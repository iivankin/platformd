use crate::{
    bucket::{is_managed_bucket, physical_bucket_name},
    buffer_pool::{PooledReaderStream, SizeLimitedStream},
    data_plane::{DataPlane, MaintenanceMode, ProjectConfig},
    largest_objects::LargestObjectSearches,
    traffic::{self, TrafficCounters, TrafficRegistry},
    usage,
};
use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use bytes::Bytes;
use futures_util::TryStreamExt as _;
use http::{HeaderMap, HeaderValue, Method, Request, Response, StatusCode};
use http_body_util::{BodyExt, Full, Limited, StreamBody, combinators::BoxBody};
use hyper::{body::Frame, body::Incoming};
use rustfs_ecstore::api::{
    error::StorageError,
    object::{ObjectInfo, ObjectOptions, PutObjReader},
    storage::ECStore,
};
use rustfs_rio::HashReader;
use rustfs_storage_api::{
    BucketOperations as _, BucketOptions, DeleteBucketOptions, HTTPPreconditions, HTTPRangeSpec,
    ListOperations as _, MakeBucketOptions, MultipartOperations as _, ObjectIO as _,
    ObjectOperations as _, ObjectToDelete,
};
use serde::{Deserialize, Serialize};
use std::{
    collections::{HashMap, HashSet},
    convert::Infallible,
    io,
    sync::Arc,
    time::Instant,
};
use time::OffsetDateTime;
use tokio_util::io::StreamReader;

type BoxError = Box<dyn std::error::Error + Send + Sync>;
type Body = BoxBody<Bytes, BoxError>;

const MAX_JSON: usize = 4 << 20;
const SMALL_STREAM_BUFFER: usize = 32 << 10;
const LARGE_STREAM_BUFFER: usize = 1 << 20;
const LARGE_STREAM_THRESHOLD: i64 = 1 << 20;
const MAX_OBJECT_SIZE: i64 = 100 << 30;

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct ObjectRecord {
    key: String,
    content_type: String,
    etag: String,
    size: i64,
    updated_at_millis: i64,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct ListResult {
    objects: Vec<ObjectRecord>,
    prefixes: Vec<String>,
    more: bool,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ReconcileBucketsInput {
    store_ids: Vec<String>,
}

#[derive(Debug, Serialize)]
struct ErrorResponse<'a> {
    code: &'a str,
    message: String,
}

pub async fn handle(
    store: Arc<ECStore>,
    data_plane: DataPlane,
    largest_objects: LargestObjectSearches,
    traffic: Arc<TrafficRegistry>,
    request: Request<Incoming>,
) -> Result<Response<Body>, Infallible> {
    let response = match dispatch(store, data_plane, largest_objects, traffic, request).await {
        Ok(response) => response,
        Err(error) => error_response(error),
    };
    Ok(response)
}

async fn dispatch(
    store: Arc<ECStore>,
    data_plane: DataPlane,
    largest_objects: LargestObjectSearches,
    traffic: Arc<TrafficRegistry>,
    request: Request<Incoming>,
) -> Result<Response<Body>, ApiError> {
    let method = request.method().clone();
    let path = request.uri().path().to_owned();
    if method == Method::GET && path == "/health" {
        return Ok(empty(StatusCode::NO_CONTENT));
    }
    if method == Method::PUT && path == "/v1/data-plane/project" {
        let config: ProjectConfig = json_body(request.into_body()).await?;
        data_plane
            .configure(config)
            .await
            .map_err(|error| ApiError::invalid(&error))?;
        return Ok(empty(StatusCode::NO_CONTENT));
    }
    if method == Method::DELETE && path == "/v1/data-plane/project" {
        let project_id = required_header(request.headers(), "x-platformd-project")?;
        data_plane.remove(&project_id).await;
        return Ok(empty(StatusCode::NO_CONTENT));
    }
    if method == Method::POST && path == "/v1/data-plane/store/backup/begin" {
        let store_id = required_header(request.headers(), "x-platformd-store")?;
        data_plane
            .begin_store_maintenance(&store_id, MaintenanceMode::Backup)
            .await
            .map_err(|error| ApiError::invalid(&error))?;
        return Ok(empty(StatusCode::NO_CONTENT));
    }
    if method == Method::POST && path == "/v1/data-plane/store/backup/end" {
        let store_id = required_header(request.headers(), "x-platformd-store")?;
        data_plane
            .end_store_maintenance(&store_id, MaintenanceMode::Backup)
            .await
            .map_err(|error| ApiError::invalid(&error))?;
        return Ok(empty(StatusCode::NO_CONTENT));
    }
    if method == Method::POST && path == "/v1/data-plane/store/restore/begin" {
        let store_id = required_header(request.headers(), "x-platformd-store")?;
        let bucket = physical_bucket_name(&store_id).map_err(|error| ApiError::invalid(&error))?;
        largest_objects.cancel(&bucket).await;
        data_plane
            .begin_store_maintenance(&store_id, MaintenanceMode::Restore)
            .await
            .map_err(|error| ApiError::invalid(&error))?;
        return Ok(empty(StatusCode::NO_CONTENT));
    }
    if method == Method::POST && path == "/v1/data-plane/store/restore/end" {
        let store_id = required_header(request.headers(), "x-platformd-store")?;
        data_plane
            .end_store_maintenance(&store_id, MaintenanceMode::Restore)
            .await
            .map_err(|error| ApiError::invalid(&error))?;
        return Ok(empty(StatusCode::NO_CONTENT));
    }
    if method == Method::POST && path == "/v1/data-plane/quiesce/begin" {
        data_plane
            .begin_quiesce()
            .await
            .map_err(|error| ApiError::invalid(&error))?;
        return Ok(empty(StatusCode::NO_CONTENT));
    }
    if method == Method::POST && path == "/v1/data-plane/quiesce/end" {
        data_plane
            .end_quiesce()
            .await
            .map_err(|error| ApiError::invalid(&error))?;
        return Ok(empty(StatusCode::NO_CONTENT));
    }
    if method == Method::POST && path == "/v1/buckets/reconcile" {
        let input: ReconcileBucketsInput = json_body(request.into_body()).await?;
        reconcile_buckets(store, &largest_objects, input.store_ids).await?;
        return Ok(empty(StatusCode::NO_CONTENT));
    }
    let bucket = physical_bucket(request.headers())?;
    let traffic_counters = traffic.for_bucket(&bucket);
    match (method, path.as_str()) {
        (Method::POST, "/v1/bucket/ensure") => {
            match store
                .make_bucket(&bucket, &MakeBucketOptions::default())
                .await
            {
                Ok(()) | Err(StorageError::BucketExists(_)) => {
                    usage::record_dirty(&bucket);
                    Ok(empty(StatusCode::NO_CONTENT))
                }
                Err(error) => Err(error.into()),
            }
        }
        (Method::GET, "/v1/bucket/stats") => {
            let stats = usage::bucket_stats(store, &bucket, traffic.snapshot(&bucket)).await?;
            json_response(StatusCode::OK, &stats)
        }
        (Method::GET, "/v1/bucket/largest-objects") => {
            let search = largest_objects.status(&bucket).await;
            json_response(StatusCode::OK, &search)
        }
        (Method::POST, "/v1/bucket/largest-objects") => {
            let search = largest_objects.start(store, bucket).await;
            json_response(StatusCode::ACCEPTED, &search)
        }
        (Method::DELETE, "/v1/bucket/largest-objects") => {
            let search = largest_objects.cancel(&bucket).await;
            json_response(StatusCode::OK, &search)
        }
        (Method::POST, "/v1/bucket/clear") => {
            largest_objects.cancel(&bucket).await;
            clear_bucket(store, &bucket).await?;
            usage::record_dirty(&bucket);
            Ok(empty(StatusCode::NO_CONTENT))
        }
        (Method::DELETE, "/v1/bucket") => {
            largest_objects.cancel(&bucket).await;
            match clear_bucket(store.clone(), &bucket).await {
                Ok(()) => {}
                Err(StorageError::BucketNotFound(_)) => return Ok(empty(StatusCode::NO_CONTENT)),
                Err(error) => return Err(error.into()),
            }
            match store
                .delete_bucket(
                    &bucket,
                    &DeleteBucketOptions {
                        no_recreate: true,
                        force_if_empty: true,
                        ..Default::default()
                    },
                )
                .await
            {
                Ok(()) | Err(StorageError::BucketNotFound(_)) => Ok(empty(StatusCode::NO_CONTENT)),
                Err(error) => Err(error.into()),
            }
        }
        (Method::HEAD, "/v1/object") => {
            with_traffic(traffic_counters.clone(), &Method::HEAD, "/v1/object", 0, || async {
                let key = decoded_header(request.headers(), "x-platformd-key")?;
                let info = store
                    .get_object_info(&bucket, &key, &ObjectOptions::default())
                    .await?;
                // HEAD transfers metadata only; do not attribute object size as bytes_out.
                Ok((object_response(StatusCode::NO_CONTENT, &info)?, 0))
            })
            .await
        }
        (Method::GET, "/v1/object") => {
            let range_length = integer_header(request.headers(), "x-platformd-length", -1)?;
            with_traffic(traffic_counters.clone(), &Method::GET, "/v1/object", 0, || async {
                let response = get_object(store, &bucket, request).await?;
                let bytes_out = if range_length >= 0 {
                    range_length as u64
                } else {
                    header_u64(response.headers(), "x-platformd-size").unwrap_or(0)
                };
                Ok((response, bytes_out))
            })
            .await
        }
        (Method::PUT, "/v1/object") => {
            let bytes_in = integer_header(request.headers(), "x-platformd-size", 0)?.max(0) as u64;
            with_traffic(traffic_counters.clone(), &Method::PUT, "/v1/object", bytes_in, || async {
                let response = put_object(store, &bucket, request).await?;
                Ok((response, 0))
            })
            .await
        }
        (Method::DELETE, "/v1/object") => {
            with_traffic(traffic_counters.clone(), &Method::DELETE, "/v1/object", 0, || async {
                let key = decoded_header(request.headers(), "x-platformd-key")?;
                let info = store
                    .delete_object(&bucket, &key, ObjectOptions::default())
                    .await?;
                usage::record_delete(&bucket, &info).await;
                Ok((empty(StatusCode::NO_CONTENT), 0))
            })
            .await
        }
        (Method::GET, "/v1/objects") => {
            with_traffic(traffic_counters.clone(), &Method::GET, "/v1/objects", 0, || async {
                let response = list_objects(store, &bucket, request.headers()).await?;
                Ok((response, 0))
            })
            .await
        }
        _ => Err(ApiError::new(
            StatusCode::NOT_FOUND,
            "not_found",
            "internal route not found",
        )),
    }
}

async fn reconcile_buckets(
    store: Arc<ECStore>,
    largest_objects: &LargestObjectSearches,
    store_ids: Vec<String>,
) -> Result<(), ApiError> {
    let mut desired = HashSet::with_capacity(store_ids.len());
    for store_id in store_ids {
        let bucket = physical_bucket_name(&store_id).map_err(|error| ApiError::invalid(&error))?;
        if !desired.insert(bucket) {
            return Err(ApiError::invalid("duplicate object store identity"));
        }
    }
    let existing: HashSet<String> = store
        .list_bucket(&BucketOptions {
            no_metadata: true,
            ..Default::default()
        })
        .await?
        .into_iter()
        .map(|bucket| bucket.name)
        .collect();

    for bucket in desired.difference(&existing) {
        match store
            .make_bucket(bucket, &MakeBucketOptions::default())
            .await
        {
            Ok(()) | Err(StorageError::BucketExists(_)) => {}
            Err(error) => return Err(error.into()),
        }
    }
    for bucket in existing
        .difference(&desired)
        .filter(|bucket| is_managed_bucket(bucket))
    {
        largest_objects.cancel(bucket).await;
        clear_bucket(store.clone(), bucket).await?;
        store
            .delete_bucket(
                bucket,
                &DeleteBucketOptions {
                    no_recreate: true,
                    force_if_empty: true,
                    ..Default::default()
                },
            )
            .await?;
    }
    Ok(())
}

async fn get_object(
    store: Arc<ECStore>,
    bucket: &str,
    request: Request<Incoming>,
) -> Result<Response<Body>, ApiError> {
    let key = decoded_header(request.headers(), "x-platformd-key")?;
    let offset = integer_header(request.headers(), "x-platformd-offset", 0)?;
    let length = integer_header(request.headers(), "x-platformd-length", -1)?;
    let range = if length >= 0 {
        Some(HTTPRangeSpec {
            is_suffix_length: false,
            start: offset,
            end: offset
                .checked_add(length)
                .and_then(|end| end.checked_sub(1))
                .ok_or_else(|| {
                    ApiError::new(StatusCode::BAD_REQUEST, "invalid_input", "range overflows")
                })?,
        })
    } else {
        None
    };
    let reader = store
        .get_object_reader(
            bucket,
            &key,
            range,
            HeaderMap::new(),
            &ObjectOptions::default(),
        )
        .await?;
    let info = reader.object_info.clone();
    let response_size = if length >= 0 { length } else { info.size };
    let stream = PooledReaderStream::new(reader.stream, stream_buffer_size(response_size))
        .map_ok(Frame::data)
        .map_err(|error| -> BoxError { Box::new(error) });
    let body = http_body_util::BodyExt::boxed(StreamBody::new(stream));
    let mut response = Response::new(body);
    *response.status_mut() = StatusCode::OK;
    apply_object_headers(response.headers_mut(), &info)?;
    Ok(response)
}

fn stream_buffer_size(size: i64) -> usize {
    if size >= LARGE_STREAM_THRESHOLD {
        LARGE_STREAM_BUFFER
    } else {
        SMALL_STREAM_BUFFER
    }
}

async fn put_object(
    store: Arc<ECStore>,
    bucket: &str,
    request: Request<Incoming>,
) -> Result<Response<Body>, ApiError> {
    let key = decoded_header(request.headers(), "x-platformd-key")?;
    let content_type = decoded_optional_header(request.headers(), "x-platformd-content-type")?;
    let expected_sha = optional_header(request.headers(), "x-platformd-sha256")?;
    let size = integer_header(request.headers(), "x-platformd-size", -1)?;
    if size > MAX_OBJECT_SIZE {
        return Err(ApiError::new(
            StatusCode::PAYLOAD_TOO_LARGE,
            "object_too_large",
            "object payload exceeds the platformd size limit",
        ));
    }
    let if_match = optional_header(request.headers(), "x-platformd-if-match")?;
    let if_none_match = bool_header(request.headers(), "x-platformd-if-none-match")?;
    let preserve_etag = nonempty(decoded_optional_header(
        request.headers(),
        "x-platformd-preserve-etag",
    )?);
    let mod_time = optional_integer_header(request.headers(), "x-platformd-mod-time")?
        .map(|millis| {
            millis
                .checked_mul(1_000_000)
                .ok_or_else(|| ApiError::invalid("modification time overflows"))
                .and_then(|nanos| {
                    OffsetDateTime::from_unix_timestamp_nanos(nanos)
                        .map_err(|_| ApiError::invalid("modification time is invalid"))
                })
        })
        .transpose()?;
    let options = object_options(
        &content_type,
        if_match,
        if_none_match,
        preserve_etag,
        mod_time,
    );
    let stream = request
        .into_body()
        .into_data_stream()
        .map_err(io::Error::other);
    let stream = SizeLimitedStream::new(
        stream,
        u64::try_from(MAX_OBJECT_SIZE).expect("positive object size limit"),
    );
    let reader = StreamReader::new(stream);
    let hash_reader =
        HashReader::from_stream(reader, size, size, None, nonempty(expected_sha), false)?;
    let mut put_reader = PutObjReader::new(hash_reader);
    let (info, previous) = store
        .put_object_with_old_current_size(bucket, &key, &mut put_reader, &options)
        .await?;
    usage::record_write(bucket, &info, previous).await;
    object_response(StatusCode::OK, &info)
}

async fn list_objects(
    store: Arc<ECStore>,
    bucket: &str,
    headers: &HeaderMap,
) -> Result<Response<Body>, ApiError> {
    let prefix = decoded_optional_header(headers, "x-platformd-prefix")?;
    let delimiter = decoded_optional_header(headers, "x-platformd-delimiter")?;
    let after = decoded_optional_header(headers, "x-platformd-after")?;
    let limit = integer_header(headers, "x-platformd-limit", 1000)?;
    let listed = store
        .list_objects_v2(
            bucket,
            &prefix,
            None,
            nonempty(delimiter),
            i32::try_from(limit).map_err(|_| ApiError::invalid("list limit is invalid"))?,
            false,
            nonempty(after),
            false,
        )
        .await?;
    json_response(
        StatusCode::OK,
        &ListResult {
            objects: listed.objects.iter().map(object_record).collect(),
            prefixes: listed.prefixes,
            more: listed.is_truncated,
        },
    )
}

async fn clear_bucket(store: Arc<ECStore>, bucket: &str) -> Result<(), StorageError> {
    loop {
        let listed = store
            .list_multipart_uploads(bucket, "", None, None, None, 1000)
            .await?;
        if listed.uploads.is_empty() {
            break;
        }
        for upload in listed.uploads {
            store
                .abort_multipart_upload(
                    bucket,
                    &upload.object,
                    &upload.upload_id,
                    &ObjectOptions::default(),
                )
                .await?;
        }
    }
    loop {
        let listed = store
            .clone()
            .list_objects_v2(bucket, "", None, None, 1000, false, None, false)
            .await?;
        if listed.objects.is_empty() {
            return Ok(());
        }
        let objects = listed
            .objects
            .into_iter()
            .map(|object| ObjectToDelete {
                object_name: object.name,
                ..Default::default()
            })
            .collect();
        let (_, errors) = store
            .delete_objects(bucket, objects, ObjectOptions::default())
            .await;
        if let Some(error) = errors.into_iter().flatten().next() {
            return Err(error);
        }
    }
}

fn object_options(
    content_type: &str,
    if_match: String,
    if_none_match: bool,
    preserve_etag: Option<String>,
    mod_time: Option<OffsetDateTime>,
) -> ObjectOptions {
    let mut user_defined = HashMap::new();
    if !content_type.is_empty() {
        user_defined.insert("content-type".to_owned(), content_type.to_owned());
    }
    ObjectOptions {
        versioned: false,
        user_defined,
        preserve_etag,
        mod_time,
        http_preconditions: (if_none_match || !if_match.is_empty()).then_some(HTTPPreconditions {
            if_match: nonempty(if_match),
            if_none_match: if_none_match.then(|| "*".to_owned()),
            ..Default::default()
        }),
        ..Default::default()
    }
}

fn physical_bucket(headers: &HeaderMap) -> Result<String, ApiError> {
    let store_id = required_header(headers, "x-platformd-store")?;
    physical_bucket_name(&store_id).map_err(|error| ApiError::invalid(&error))
}

fn object_record(info: &ObjectInfo) -> ObjectRecord {
    ObjectRecord {
        key: info.name.clone(),
        content_type: info.content_type.clone().unwrap_or_default(),
        etag: info.etag.clone().unwrap_or_default(),
        size: info.size,
        updated_at_millis: timestamp_millis(info.mod_time),
    }
}

fn timestamp_millis(value: Option<OffsetDateTime>) -> i64 {
    value
        .and_then(|value| i64::try_from(value.unix_timestamp_nanos() / 1_000_000).ok())
        .unwrap_or_default()
}

fn apply_object_headers(headers: &mut HeaderMap, info: &ObjectInfo) -> Result<(), ApiError> {
    headers.insert("x-platformd-key", encoded_value(&info.name)?);
    headers.insert(
        "x-platformd-content-type",
        encoded_value(info.content_type.as_deref().unwrap_or_default())?,
    );
    headers.insert(
        "x-platformd-etag",
        encoded_value(info.etag.as_deref().unwrap_or_default())?,
    );
    headers.insert("x-platformd-size", integer_value(info.size)?);
    headers.insert(
        "x-platformd-updated-at",
        integer_value(timestamp_millis(info.mod_time))?,
    );
    Ok(())
}

fn object_response(status: StatusCode, info: &ObjectInfo) -> Result<Response<Body>, ApiError> {
    let mut response = empty(status);
    apply_object_headers(response.headers_mut(), info)?;
    Ok(response)
}

async fn json_body<T: for<'de> Deserialize<'de>>(body: Incoming) -> Result<T, ApiError> {
    let bytes = Limited::new(body, MAX_JSON)
        .collect()
        .await
        .map_err(|error| {
            ApiError::new(StatusCode::BAD_REQUEST, "invalid_input", error.to_string())
        })?
        .to_bytes();
    serde_json::from_slice(&bytes).map_err(|error| ApiError::invalid(&error.to_string()))
}

fn json_response<T: Serialize>(status: StatusCode, value: &T) -> Result<Response<Body>, ApiError> {
    let bytes = serde_json::to_vec(value).map_err(|error| ApiError::internal(error.to_string()))?;
    let mut response = Response::new(full(Bytes::from(bytes)));
    *response.status_mut() = status;
    response
        .headers_mut()
        .insert("content-type", HeaderValue::from_static("application/json"));
    Ok(response)
}

fn empty(status: StatusCode) -> Response<Body> {
    let mut response = Response::new(full(Bytes::new()));
    *response.status_mut() = status;
    response
}

fn full(bytes: Bytes) -> Body {
    Full::new(bytes).map_err(|never| match never {}).boxed()
}

fn error_response(error: ApiError) -> Response<Body> {
    let ApiError {
        status,
        code,
        message,
    } = error;
    let response = ErrorResponse { code, message };
    let mut result = json_response(status, &response)
        .unwrap_or_else(|_| empty(StatusCode::INTERNAL_SERVER_ERROR));
    result
        .headers_mut()
        .insert("x-platformd-error-code", HeaderValue::from_static(code));
    result
}

fn required_header(headers: &HeaderMap, name: &str) -> Result<String, ApiError> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned)
        .filter(|value| !value.is_empty())
        .ok_or_else(|| ApiError::invalid(&format!("{name} is required")))
}

fn optional_header(headers: &HeaderMap, name: &str) -> Result<String, ApiError> {
    match headers.get(name) {
        Some(value) => value
            .to_str()
            .map(str::to_owned)
            .map_err(|_| ApiError::invalid(&format!("{name} is invalid"))),
        None => Ok(String::new()),
    }
}

fn decoded_header(headers: &HeaderMap, name: &str) -> Result<String, ApiError> {
    let value = required_header(headers, name)?;
    decode_value(&value)
}

fn decoded_optional_header(headers: &HeaderMap, name: &str) -> Result<String, ApiError> {
    let value = optional_header(headers, name)?;
    if value.is_empty() {
        Ok(String::new())
    } else {
        decode_value(&value)
    }
}

fn decode_value(value: &str) -> Result<String, ApiError> {
    let bytes = URL_SAFE_NO_PAD
        .decode(value)
        .map_err(|_| ApiError::invalid("base64 value is invalid"))?;
    String::from_utf8(bytes).map_err(|_| ApiError::invalid("UTF-8 value is invalid"))
}

fn encoded_value(value: &str) -> Result<HeaderValue, ApiError> {
    HeaderValue::from_str(&URL_SAFE_NO_PAD.encode(value))
        .map_err(|_| ApiError::internal("encode response header"))
}

fn integer_header(headers: &HeaderMap, name: &str, default: i64) -> Result<i64, ApiError> {
    match headers.get(name) {
        Some(value) => value
            .to_str()
            .ok()
            .and_then(|value| value.parse().ok())
            .ok_or_else(|| ApiError::invalid(&format!("{name} is invalid"))),
        None => Ok(default),
    }
}

fn optional_integer_header(headers: &HeaderMap, name: &str) -> Result<Option<i128>, ApiError> {
    match headers.get(name) {
        Some(value) => value
            .to_str()
            .ok()
            .and_then(|value| value.parse().ok())
            .map(Some)
            .ok_or_else(|| ApiError::invalid(&format!("{name} is invalid"))),
        None => Ok(None),
    }
}

fn bool_header(headers: &HeaderMap, name: &str) -> Result<bool, ApiError> {
    match headers.get(name) {
        None => Ok(false),
        Some(value) if value == "true" => Ok(true),
        Some(value) if value == "false" => Ok(false),
        Some(_) => Err(ApiError::invalid(&format!("{name} is invalid"))),
    }
}

fn integer_value(value: i64) -> Result<HeaderValue, ApiError> {
    HeaderValue::from_str(&value.to_string())
        .map_err(|_| ApiError::internal("encode integer response header"))
}

fn nonempty(value: String) -> Option<String> {
    (!value.is_empty()).then_some(value)
}

async fn with_traffic<F, Fut>(
    traffic: Arc<TrafficCounters>,
    method: &Method,
    path: &str,
    bytes_in: u64,
    work: F,
) -> Result<Response<Body>, ApiError>
where
    F: FnOnce() -> Fut,
    Fut: std::future::Future<Output = Result<(Response<Body>, u64), ApiError>>,
{
    let op = traffic::classify_control(method, path).unwrap_or(traffic::OpClass::Other);
    traffic.record_start();
    let started = Instant::now();
    match work().await {
        Ok((response, bytes_out)) => {
            let error = !response.status().is_success();
            let bytes_out = Arc::new(std::sync::atomic::AtomicU64::new(bytes_out));
            let pending = traffic::PendingFinish::new(
                traffic,
                op,
                bytes_in,
                bytes_out,
                started,
                error,
            );
            let (parts, body) = response.into_parts();
            // Keep pending alive until the body is dropped so active_requests and
            // latency cover streaming control-plane downloads.
            let body = BodyExt::boxed(body.map_frame(move |frame| {
                let _pending = &pending;
                frame
            }));
            Ok(Response::from_parts(parts, body))
        }
        Err(error) => {
            traffic.record_finish(op, bytes_in, 0, started.elapsed(), true);
            Err(error)
        }
    }
}

fn header_u64(headers: &HeaderMap, name: &str) -> Option<u64> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse().ok())
}

struct ApiError {
    status: StatusCode,
    code: &'static str,
    message: String,
}

impl ApiError {
    fn new(status: StatusCode, code: &'static str, message: impl Into<String>) -> Self {
        Self {
            status,
            code,
            message: message.into(),
        }
    }

    fn invalid(message: &str) -> Self {
        Self::new(StatusCode::BAD_REQUEST, "invalid_input", message)
    }

    fn internal(message: impl Into<String>) -> Self {
        Self::new(StatusCode::INTERNAL_SERVER_ERROR, "internal", message)
    }
}

impl From<io::Error> for ApiError {
    fn from(error: io::Error) -> Self {
        Self::internal(error.to_string())
    }
}

impl From<StorageError> for ApiError {
    fn from(error: StorageError) -> Self {
        let (status, code) = match &error {
            StorageError::ObjectNotFound(_, _)
            | StorageError::FileNotFound
            | StorageError::FileVersionNotFound
            | StorageError::VersionNotFound(_, _, _) => (StatusCode::NOT_FOUND, "object_not_found"),
            StorageError::PreconditionFailed => {
                (StatusCode::PRECONDITION_FAILED, "precondition_failed")
            }
            StorageError::InvalidPart(_, _, _)
            | StorageError::InvalidPartNumber(_)
            | StorageError::EntityTooSmall(_, _, _) => (StatusCode::BAD_REQUEST, "invalid_part"),
            StorageError::LessData | StorageError::MoreData | StorageError::ShortWrite => {
                (StatusCode::BAD_REQUEST, "bad_digest")
            }
            StorageError::StorageFull | StorageError::DiskFull => {
                (StatusCode::INSUFFICIENT_STORAGE, "storage_full")
            }
            _ => (StatusCode::INTERNAL_SERVER_ERROR, "internal"),
        };
        Self::new(status, code, error.to_string())
    }
}
