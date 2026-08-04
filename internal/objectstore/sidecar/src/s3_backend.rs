use crate::{
    buffer_pool::{PooledReaderStream, SizeLimitedStream},
    data_plane::{ProjectState, RequestLease, ResolvedStore},
    usage,
};
use async_trait::async_trait;
use futures_util::TryStreamExt as _;
use http::HeaderMap;
use rustfs_ecstore::api::{
    error::StorageError,
    object::{ObjectInfo, ObjectOptions, PutObjReader},
    storage::ECStore,
};
use rustfs_rio::HashReader;
use rustfs_storage_api::{
    BucketOperations as _, BucketOptions, CompletePart, HTTPPreconditions, HTTPRangeSpec,
    ListOperations as _, MultipartOperations as _, ObjectIO as _, ObjectOperations as _,
    ObjectToDelete,
};
use s3s::stream::ByteStream as _;
use s3s::{S3, S3Request, S3Response, S3Result, dto::*};
use std::{collections::HashMap, io, ops::Deref, sync::Arc};
use tokio_util::io::StreamReader;

const SMALL_STREAM_BUFFER: usize = 32 << 10;
const LARGE_STREAM_BUFFER: usize = 1 << 20;
const LARGE_STREAM_THRESHOLD: i64 = 1 << 20;
const PUBLIC_STORE_HEADER: &str = "x-platformd-public-store";
const MAX_OBJECT_SIZE: i64 = 100 << 30;
const MAX_MULTIPART_PART_SIZE: i64 = 512 << 20;

struct BucketAccess {
    store: ResolvedStore,
    _lease: RequestLease,
}

// The access object keeps both the immutable routing snapshot and maintenance
// admission alive for the full storage operation while borrowing the bucket name.
impl Deref for BucketAccess {
    type Target = str;

    fn deref(&self) -> &Self::Target {
        self.store.physical_bucket()
    }
}

#[derive(Clone)]
pub struct S3Backend {
    store: Arc<ECStore>,
    project: Arc<ProjectState>,
}

impl S3Backend {
    pub fn new(store: Arc<ECStore>, project: Arc<ProjectState>) -> Self {
        Self { store, project }
    }

    async fn physical_bucket<T>(
        &self,
        req: &S3Request<T>,
        bucket: &str,
        write: bool,
    ) -> S3Result<BucketAccess> {
        let credentials = req
            .credentials
            .as_ref()
            .ok_or_else(|| s3s::s3_error!(AccessDenied))?;
        let configured = self
            .project
            .store_for_access_key(&credentials.access_key)
            .ok_or_else(|| s3s::s3_error!(InvalidAccessKeyId))?;
        if configured.bucket_name() != bucket {
            return Err(s3s::s3_error!(NoSuchBucket));
        }
        if let Some(public_store) = req.headers.get(PUBLIC_STORE_HEADER) {
            let public_store = public_store
                .to_str()
                .map_err(|_| s3s::s3_error!(AccessDenied))?;
            if public_store != configured.store_id() {
                return Err(s3s::s3_error!(NoSuchBucket));
            }
        }
        if write && !configured.can_write() {
            return Err(s3s::s3_error!(AccessDenied, "credential is read-only"));
        }
        let lease = configured.enter(write).await?;
        Ok(BucketAccess {
            store: configured,
            _lease: lease,
        })
    }
}

#[async_trait]
impl S3 for S3Backend {
    async fn head_bucket(
        &self,
        req: S3Request<HeadBucketInput>,
    ) -> S3Result<S3Response<HeadBucketOutput>> {
        let access = self.physical_bucket(&req, &req.input.bucket, false).await?;
        self.store
            .get_bucket_info(&access, &BucketOptions::default())
            .await
            .map_err(storage_error)?;
        Ok(S3Response::new(HeadBucketOutput {
            bucket_region: Some("us-east-1".to_owned()),
            ..Default::default()
        }))
    }

    async fn get_bucket_location(
        &self,
        req: S3Request<GetBucketLocationInput>,
    ) -> S3Result<S3Response<GetBucketLocationOutput>> {
        let access = self.physical_bucket(&req, &req.input.bucket, false).await?;
        self.store
            .get_bucket_info(&access, &BucketOptions::default())
            .await
            .map_err(storage_error)?;
        Ok(S3Response::new(GetBucketLocationOutput::default()))
    }

    async fn put_object(
        &self,
        req: S3Request<PutObjectInput>,
    ) -> S3Result<S3Response<PutObjectOutput>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, true).await?;
        let PutObjectInput {
            body,
            key,
            content_length,
            content_type,
            content_encoding,
            metadata,
            if_match,
            if_none_match,
            ..
        } = req.input;
        let body = body.ok_or_else(|| s3s::s3_error!(IncompleteBody))?;
        let size = content_length.unwrap_or_else(|| {
            body.remaining_length()
                .exact()
                .and_then(|length| i64::try_from(length).ok())
                .unwrap_or(-1)
        });
        ensure_size(size, MAX_OBJECT_SIZE)?;
        let stream = SizeLimitedStream::new(
            body.map_err(io::Error::other),
            u64::try_from(MAX_OBJECT_SIZE).expect("positive object size limit"),
        );
        let reader = StreamReader::new(stream);
        let hash_reader = HashReader::from_stream(reader, size, size, None, None, false)
            .map_err(storage_error)?;
        let mut put_reader = PutObjReader::new(hash_reader);
        let options = write_options(
            content_type,
            strip_aws_chunked(content_encoding),
            metadata,
            if_match,
            if_none_match,
        );
        let (info, previous) = self
            .store
            .put_object_with_old_current_size(&bucket, &key, &mut put_reader, &options)
            .await
            .map_err(storage_error)?;
        usage::record_write(&bucket, &info, previous).await;
        Ok(S3Response::new(PutObjectOutput {
            e_tag: object_etag(&info),
            size: Some(info.size),
            ..Default::default()
        }))
    }

    async fn head_object(
        &self,
        req: S3Request<HeadObjectInput>,
    ) -> S3Result<S3Response<HeadObjectOutput>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, false).await?;
        let info = self
            .store
            .get_object_info(&bucket, &req.input.key, &ObjectOptions::default())
            .await
            .map_err(storage_error)?;
        let (content_length, content_range) = response_range(req.input.range, info.size)?;
        Ok(S3Response::new(HeadObjectOutput {
            accept_ranges: Some("bytes".to_owned()),
            content_length: Some(content_length),
            content_range,
            content_type: info.content_type.clone(),
            content_encoding: info.content_encoding.clone(),
            e_tag: object_etag(&info),
            last_modified: info.mod_time.map(Timestamp::from),
            metadata: object_metadata(&info),
            ..Default::default()
        }))
    }

    async fn get_object(
        &self,
        req: S3Request<GetObjectInput>,
    ) -> S3Result<S3Response<GetObjectOutput>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, false).await?;
        let range = req.input.range.map(storage_range);
        let reader = self
            .store
            .get_object_reader(
                &bucket,
                &req.input.key,
                range,
                HeaderMap::new(),
                &ObjectOptions::default(),
            )
            .await
            .map_err(storage_error)?;
        let info = reader.object_info;
        let (content_length, content_range) = response_range(req.input.range, info.size)?;
        // The stream owns the access lease. Restore therefore cannot clear the
        // bucket while a response body is still being consumed.
        let stream = PooledReaderStream::with_guard(
            reader.stream,
            stream_buffer_size(content_length),
            bucket,
        );
        Ok(S3Response::new(GetObjectOutput {
            accept_ranges: Some("bytes".to_owned()),
            body: Some(StreamingBlob::wrap(stream)),
            content_length: Some(content_length),
            content_range,
            content_type: info.content_type.clone(),
            content_encoding: info.content_encoding.clone(),
            e_tag: object_etag(&info),
            last_modified: info.mod_time.map(Timestamp::from),
            metadata: object_metadata(&info),
            ..Default::default()
        }))
    }

    async fn list_objects_v2(
        &self,
        req: S3Request<ListObjectsV2Input>,
    ) -> S3Result<S3Response<ListObjectsV2Output>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, false).await?;
        let max_keys = req.input.max_keys.unwrap_or(1000).clamp(0, 1000);
        let listed = self
            .store
            .clone()
            .list_objects_v2(
                &bucket,
                req.input.prefix.as_deref().unwrap_or(""),
                req.input.continuation_token.clone(),
                req.input.delimiter.clone(),
                max_keys,
                false,
                req.input.start_after.clone(),
                false,
            )
            .await
            .map_err(storage_error)?;
        let contents: Vec<Object> = listed.objects.iter().map(s3_object).collect();
        let prefixes: Vec<CommonPrefix> = listed
            .prefixes
            .into_iter()
            .map(|prefix| CommonPrefix {
                prefix: Some(prefix),
            })
            .collect();
        let key_count = i32::try_from(contents.len() + prefixes.len())
            .map_err(|_| s3s::s3_error!(InternalError))?;
        Ok(S3Response::new(ListObjectsV2Output {
            name: Some(req.input.bucket),
            prefix: req.input.prefix,
            max_keys: Some(max_keys),
            key_count: Some(key_count),
            continuation_token: req.input.continuation_token,
            is_truncated: Some(listed.is_truncated),
            next_continuation_token: listed.next_continuation_token,
            contents: (!contents.is_empty()).then_some(contents),
            common_prefixes: (!prefixes.is_empty()).then_some(prefixes),
            delimiter: req.input.delimiter,
            encoding_type: req.input.encoding_type,
            start_after: req.input.start_after,
            ..Default::default()
        }))
    }

    async fn copy_object(
        &self,
        req: S3Request<CopyObjectInput>,
    ) -> S3Result<S3Response<CopyObjectOutput>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, true).await?;
        let replace_metadata = req
            .input
            .metadata_directive
            .as_ref()
            .is_some_and(|directive| directive.as_str() == MetadataDirective::REPLACE);
        let (source_bucket, source_key) = match &req.input.copy_source {
            CopySource::Bucket { bucket, key, .. } => (bucket.as_ref(), key.as_ref()),
            CopySource::AccessPoint { .. } | CopySource::Outpost { .. } => {
                return Err(s3s::s3_error!(NotImplemented));
            }
        };
        if source_bucket != req.input.bucket {
            return Err(s3s::s3_error!(
                AccessDenied,
                "cross-bucket copy is not supported"
            ));
        }
        let source_options = ObjectOptions {
            http_preconditions: http_preconditions(
                req.input.copy_source_if_match.clone(),
                req.input.copy_source_if_none_match.clone(),
            ),
            ..Default::default()
        };
        let source_reader = self
            .store
            .get_object_reader(&bucket, source_key, None, HeaderMap::new(), &source_options)
            .await
            .map_err(storage_error)?;
        let source_size = source_reader.object_info.size;
        ensure_size(source_size, MAX_OBJECT_SIZE)?;
        let hash_reader = HashReader::from_stream(
            source_reader.stream,
            source_size,
            source_size,
            None,
            None,
            false,
        )
        .map_err(storage_error)?;
        let mut put_reader = PutObjReader::new(hash_reader);
        let destination_options = if replace_metadata {
            write_options(
                req.input.content_type.clone(),
                strip_aws_chunked(req.input.content_encoding.clone()),
                req.input.metadata.clone(),
                None,
                None,
            )
        } else {
            ObjectOptions {
                user_defined: (*source_reader.object_info.user_defined).clone(),
                ..Default::default()
            }
        };
        let (info, previous) = self
            .store
            .put_object_with_old_current_size(
                &bucket,
                &req.input.key,
                &mut put_reader,
                &destination_options,
            )
            .await
            .map_err(storage_error)?;
        usage::record_write(&bucket, &info, previous).await;
        Ok(S3Response::new(CopyObjectOutput {
            copy_object_result: Some(CopyObjectResult {
                e_tag: object_etag(&info),
                last_modified: info.mod_time.map(Timestamp::from),
                ..Default::default()
            }),
            ..Default::default()
        }))
    }

    async fn delete_object(
        &self,
        req: S3Request<DeleteObjectInput>,
    ) -> S3Result<S3Response<DeleteObjectOutput>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, true).await?;
        match self
            .store
            .delete_object(&bucket, &req.input.key, ObjectOptions::default())
            .await
        {
            Ok(info) => {
                usage::record_delete(&bucket, &info).await;
                Ok(S3Response::new(DeleteObjectOutput::default()))
            }
            Err(StorageError::ObjectNotFound(_, _)) => {
                Ok(S3Response::new(DeleteObjectOutput::default()))
            }
            Err(error) => Err(storage_error(error)),
        }
    }

    async fn delete_objects(
        &self,
        req: S3Request<DeleteObjectsInput>,
    ) -> S3Result<S3Response<DeleteObjectsOutput>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, true).await?;
        let requested = req.input.delete.objects;
        let objects = requested
            .iter()
            .map(|object| ObjectToDelete {
                object_name: object.key.clone(),
                ..Default::default()
            })
            .collect();
        let (_, failures) = self
            .store
            .delete_objects(&bucket, objects, ObjectOptions::default())
            .await;
        if failures.iter().any(Option::is_none) {
            // The ECStore bulk-delete result does not expose deleted sizes, so
            // the scanner reconciles the aggregate without extra HEAD requests.
            usage::record_dirty(&bucket);
        }
        let mut deleted = Vec::new();
        let mut errors = Vec::new();
        for (object, failure) in requested.into_iter().zip(failures) {
            if let Some(failure) = failure {
                errors.push(Error {
                    code: Some("InternalError".to_owned()),
                    key: Some(object.key),
                    message: Some(failure.to_string()),
                    ..Default::default()
                });
            } else if !req.input.delete.quiet.unwrap_or(false) {
                deleted.push(DeletedObject {
                    key: Some(object.key),
                    ..Default::default()
                });
            }
        }
        Ok(S3Response::new(DeleteObjectsOutput {
            deleted: (!deleted.is_empty()).then_some(deleted),
            errors: (!errors.is_empty()).then_some(errors),
            ..Default::default()
        }))
    }

    async fn create_multipart_upload(
        &self,
        req: S3Request<CreateMultipartUploadInput>,
    ) -> S3Result<S3Response<CreateMultipartUploadOutput>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, true).await?;
        let options = write_options(
            req.input.content_type.clone(),
            strip_aws_chunked(req.input.content_encoding.clone()),
            req.input.metadata.clone(),
            None,
            None,
        );
        let result = self
            .store
            .new_multipart_upload(&bucket, &req.input.key, &options)
            .await
            .map_err(storage_error)?;
        Ok(S3Response::new(CreateMultipartUploadOutput {
            bucket: Some(req.input.bucket),
            key: Some(req.input.key),
            upload_id: Some(result.upload_id),
            ..Default::default()
        }))
    }

    async fn upload_part(
        &self,
        req: S3Request<UploadPartInput>,
    ) -> S3Result<S3Response<UploadPartOutput>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, true).await?;
        let body = req
            .input
            .body
            .ok_or_else(|| s3s::s3_error!(IncompleteBody))?;
        let size = req.input.content_length.unwrap_or_else(|| {
            body.remaining_length()
                .exact()
                .and_then(|length| i64::try_from(length).ok())
                .unwrap_or(-1)
        });
        ensure_size(size, MAX_MULTIPART_PART_SIZE)?;
        let part_number =
            usize::try_from(req.input.part_number).map_err(|_| s3s::s3_error!(InvalidPart))?;
        let stream = SizeLimitedStream::new(
            body.map_err(io::Error::other),
            u64::try_from(MAX_MULTIPART_PART_SIZE).expect("positive multipart size limit"),
        );
        let reader = StreamReader::new(stream);
        let hash_reader = HashReader::from_stream(reader, size, size, None, None, false)
            .map_err(storage_error)?;
        let mut put_reader = PutObjReader::new(hash_reader);
        let part = self
            .store
            .put_object_part(
                &bucket,
                &req.input.key,
                &req.input.upload_id,
                part_number,
                &mut put_reader,
                &ObjectOptions::default(),
            )
            .await
            .map_err(storage_error)?;
        Ok(S3Response::new(UploadPartOutput {
            e_tag: part.etag.map(strong_etag),
            ..Default::default()
        }))
    }

    async fn list_parts(
        &self,
        req: S3Request<ListPartsInput>,
    ) -> S3Result<S3Response<ListPartsOutput>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, false).await?;
        let marker = usize::try_from(req.input.part_number_marker.unwrap_or(0))
            .map_err(|_| s3s::s3_error!(InvalidArgument))?;
        let max_parts = usize::try_from(req.input.max_parts.unwrap_or(1000).clamp(0, 1000))
            .map_err(|_| s3s::s3_error!(InvalidArgument))?;
        let listed = self
            .store
            .list_object_parts(
                &bucket,
                &req.input.key,
                &req.input.upload_id,
                Some(marker),
                max_parts,
                &ObjectOptions::default(),
            )
            .await
            .map_err(storage_error)?;
        let parts = listed
            .parts
            .into_iter()
            .map(|part| Part {
                e_tag: part.etag.map(strong_etag),
                last_modified: part.last_mod.map(Timestamp::from),
                part_number: i32::try_from(part.part_num).ok(),
                size: i64::try_from(part.size).ok(),
                ..Default::default()
            })
            .collect();
        Ok(S3Response::new(ListPartsOutput {
            bucket: Some(req.input.bucket),
            key: Some(req.input.key),
            upload_id: Some(req.input.upload_id),
            part_number_marker: i32::try_from(listed.part_number_marker).ok(),
            next_part_number_marker: i32::try_from(listed.next_part_number_marker).ok(),
            max_parts: i32::try_from(listed.max_parts).ok(),
            is_truncated: Some(listed.is_truncated),
            parts: Some(parts),
            ..Default::default()
        }))
    }

    async fn complete_multipart_upload(
        &self,
        req: S3Request<CompleteMultipartUploadInput>,
    ) -> S3Result<S3Response<CompleteMultipartUploadOutput>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, true).await?;
        let previous = usage::previous_size(self.store.as_ref(), &bucket, &req.input.key)
            .await
            .map_err(storage_error)?;
        let parts = req
            .input
            .multipart_upload
            .and_then(|upload| upload.parts)
            .ok_or_else(|| s3s::s3_error!(InvalidPart))?
            .into_iter()
            .map(|part| {
                let part_num = part
                    .part_number
                    .ok_or_else(|| s3s::s3_error!(InvalidPart))?;
                Ok(CompletePart {
                    part_num: usize::try_from(part_num).map_err(|_| s3s::s3_error!(InvalidPart))?,
                    etag: part.e_tag.map(ETag::into_value),
                    checksum_crc32: part.checksum_crc32,
                    checksum_crc32c: part.checksum_crc32c,
                    checksum_sha1: part.checksum_sha1,
                    checksum_sha256: part.checksum_sha256,
                    checksum_crc64nvme: part.checksum_crc64nvme,
                })
            })
            .collect::<S3Result<Vec<_>>>()?;
        let listed = self
            .store
            .list_object_parts(
                &bucket,
                &req.input.key,
                &req.input.upload_id,
                None,
                10_000,
                &ObjectOptions::default(),
            )
            .await
            .map_err(storage_error)?;
        if listed.is_truncated {
            return Err(s3s::s3_error!(InvalidPart));
        }
        let part_sizes: HashMap<usize, usize> = listed
            .parts
            .into_iter()
            .map(|part| (part.part_num, part.size))
            .collect();
        let mut completed_size = 0_i64;
        for part in &parts {
            let size = part_sizes
                .get(&part.part_num)
                .copied()
                .ok_or_else(|| s3s::s3_error!(InvalidPart))?;
            completed_size = completed_size
                .checked_add(i64::try_from(size).map_err(|_| s3s::s3_error!(EntityTooLarge))?)
                .ok_or_else(|| s3s::s3_error!(EntityTooLarge))?;
        }
        ensure_size(completed_size, MAX_OBJECT_SIZE)?;
        let options = ObjectOptions {
            http_preconditions: http_preconditions(req.input.if_match, req.input.if_none_match),
            ..Default::default()
        };
        let info = self
            .store
            .clone()
            .complete_multipart_upload(
                &bucket,
                &req.input.key,
                &req.input.upload_id,
                parts,
                &options,
            )
            .await
            .map_err(storage_error)?;
        usage::record_write_with_previous(&bucket, &info, previous).await;
        Ok(S3Response::new(CompleteMultipartUploadOutput {
            bucket: Some(req.input.bucket),
            key: Some(req.input.key),
            e_tag: object_etag(&info),
            ..Default::default()
        }))
    }

    async fn abort_multipart_upload(
        &self,
        req: S3Request<AbortMultipartUploadInput>,
    ) -> S3Result<S3Response<AbortMultipartUploadOutput>> {
        let bucket = self.physical_bucket(&req, &req.input.bucket, true).await?;
        self.store
            .abort_multipart_upload(
                &bucket,
                &req.input.key,
                &req.input.upload_id,
                &ObjectOptions::default(),
            )
            .await
            .map_err(storage_error)?;
        Ok(S3Response::new(AbortMultipartUploadOutput::default()))
    }
}

fn stream_buffer_size(size: i64) -> usize {
    if size >= LARGE_STREAM_THRESHOLD {
        LARGE_STREAM_BUFFER
    } else {
        SMALL_STREAM_BUFFER
    }
}

fn ensure_size(size: i64, maximum: i64) -> S3Result<()> {
    if size > maximum {
        return Err(s3s::s3_error!(EntityTooLarge));
    }
    Ok(())
}

fn storage_range(range: Range) -> HTTPRangeSpec {
    match range {
        Range::Int { first, last } => HTTPRangeSpec {
            is_suffix_length: false,
            start: i64::try_from(first).unwrap_or(i64::MAX),
            end: last
                .and_then(|value| i64::try_from(value).ok())
                .unwrap_or(-1),
        },
        Range::Suffix { length } => HTTPRangeSpec {
            is_suffix_length: true,
            start: i64::try_from(length).unwrap_or(i64::MAX),
            end: -1,
        },
    }
}

fn response_range(range: Option<Range>, full_size: i64) -> S3Result<(i64, Option<String>)> {
    let Some(range) = range else {
        return Ok((full_size, None));
    };
    let full_size_u64 = u64::try_from(full_size).map_err(|_| s3s::s3_error!(InternalError))?;
    let checked = range.check(full_size_u64)?;
    let length =
        i64::try_from(checked.end - checked.start).map_err(|_| s3s::s3_error!(InternalError))?;
    Ok((
        length,
        Some(format!(
            "bytes {}-{}/{}",
            checked.start,
            checked.end - 1,
            full_size
        )),
    ))
}

fn write_options(
    content_type: Option<String>,
    content_encoding: Option<String>,
    metadata: Option<Metadata>,
    if_match: Option<ETagCondition>,
    if_none_match: Option<ETagCondition>,
) -> ObjectOptions {
    let mut user_defined = HashMap::new();
    if let Some(value) = content_type.as_ref() {
        user_defined.insert("content-type".to_owned(), value.clone());
    }
    if let Some(value) = content_encoding.as_ref() {
        user_defined.insert("content-encoding".to_owned(), value.clone());
    }
    if let Some(metadata) = metadata {
        for (name, value) in metadata {
            user_defined.insert(format!("x-amz-meta-{name}"), value);
        }
    }
    ObjectOptions {
        versioned: false,
        user_defined,
        http_preconditions: http_preconditions(if_match, if_none_match),
        ..Default::default()
    }
}

fn http_preconditions(
    if_match: Option<ETagCondition>,
    if_none_match: Option<ETagCondition>,
) -> Option<HTTPPreconditions> {
    if if_match.is_none() && if_none_match.is_none() {
        return None;
    }
    Some(HTTPPreconditions {
        if_match: if_match.map(condition_value),
        if_none_match: if_none_match.map(condition_value),
        ..Default::default()
    })
}

fn condition_value(condition: ETagCondition) -> String {
    match condition {
        ETagCondition::Any => "*".to_owned(),
        ETagCondition::ETag(etag) => etag.into_value(),
    }
}

fn strip_aws_chunked(content_encoding: Option<String>) -> Option<String> {
    let value = content_encoding?;
    let retained: Vec<&str> = value
        .split(',')
        .map(str::trim)
        .filter(|encoding| !encoding.eq_ignore_ascii_case("aws-chunked") && !encoding.is_empty())
        .collect();
    (!retained.is_empty()).then(|| retained.join(", "))
}

fn object_etag(info: &ObjectInfo) -> Option<ETag> {
    info.etag.clone().map(strong_etag)
}

fn object_metadata(info: &ObjectInfo) -> Option<Metadata> {
    let metadata: Metadata = info
        .user_defined
        .iter()
        .filter_map(|(name, value)| {
            let prefix = name.get(..11)?;
            prefix
                .eq_ignore_ascii_case("x-amz-meta-")
                .then(|| (name[11..].to_owned(), value.clone()))
        })
        .collect();
    (!metadata.is_empty()).then_some(metadata)
}

fn strong_etag(value: String) -> ETag {
    ETag::Strong(value.trim_matches('"').to_owned())
}

fn s3_object(info: &ObjectInfo) -> Object {
    Object {
        e_tag: object_etag(info),
        key: Some(info.name.clone()),
        last_modified: info.mod_time.map(Timestamp::from),
        size: Some(info.size),
        ..Default::default()
    }
}

fn storage_error(error: impl Into<StorageError>) -> s3s::S3Error {
    let error = error.into();
    match error {
        StorageError::BucketNotFound(_) => s3s::s3_error!(NoSuchBucket),
        StorageError::ObjectNotFound(_, _)
        | StorageError::FileNotFound
        | StorageError::FileVersionNotFound
        | StorageError::VersionNotFound(_, _, _) => s3s::s3_error!(NoSuchKey),
        StorageError::InvalidUploadID(_, _, _)
        | StorageError::InvalidUploadIDKeyCombination(_, _)
        | StorageError::MalformedUploadID(_) => s3s::s3_error!(NoSuchUpload),
        StorageError::PreconditionFailed => s3s::s3_error!(PreconditionFailed),
        StorageError::InvalidPart(_, _, _) | StorageError::InvalidPartNumber(_) => {
            s3s::s3_error!(InvalidPart)
        }
        StorageError::EntityTooSmall(_, _, _) => s3s::s3_error!(EntityTooSmall),
        StorageError::LessData | StorageError::MoreData | StorageError::ShortWrite => {
            s3s::s3_error!(BadDigest)
        }
        StorageError::StorageFull | StorageError::DiskFull => s3s::s3_error!(SlowDown),
        other => s3s::s3_error!(other, InternalError),
    }
}
