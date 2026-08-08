use crate::traffic::TrafficSnapshot;
use rustfs_ecstore::api::{
    data_usage::{
        load_data_usage_from_backend, load_data_usage_from_backend_cached,
        record_bucket_object_delete_memory, record_bucket_object_write_memory,
        record_bucket_object_write_unknown_previous_memory,
    },
    disk::OldCurrentSize,
    error::StorageError,
    object::{ObjectInfo, ObjectOptions},
    storage::ECStore,
};
use rustfs_storage_api::ObjectOperations as _;
use serde::Serialize;
use std::{collections::HashMap, sync::Arc, time::SystemTime};

const SIZE_RANGES: [(&str, &str); 7] = [
    ("LESS_THAN_1024_B", "0–1 KiB"),
    ("BETWEEN_1024B_AND_1_MB", "1 KiB–1 MiB"),
    ("BETWEEN_1_MB_AND_10_MB", "1–10 MiB"),
    ("BETWEEN_10_MB_AND_64_MB", "10–64 MiB"),
    ("BETWEEN_64_MB_AND_128_MB", "64–128 MiB"),
    ("BETWEEN_128_MB_AND_512_MB", "128–512 MiB"),
    ("GREATER_THAN_512_MB", "512 MiB+"),
];

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BucketStats {
    ready: bool,
    object_count: u64,
    total_bytes: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    observed_at: Option<u64>,
    object_size_histogram: Vec<HistogramBucket>,
    traffic: TrafficSnapshot,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct HistogramBucket {
    label: &'static str,
    count: u64,
}

impl BucketStats {
    fn warming_up(traffic: TrafficSnapshot) -> Self {
        Self {
            ready: false,
            object_count: 0,
            total_bytes: 0,
            observed_at: None,
            object_size_histogram: Vec::new(),
            traffic,
        }
    }
}

pub async fn bucket_stats(
    store: Arc<ECStore>,
    bucket: &str,
    traffic: TrafficSnapshot,
) -> Result<BucketStats, StorageError> {
    let info = match load_data_usage_from_backend_cached(store.clone()).await {
        Ok(info) => info,
        // A failed load is cached without its error kind. Retry directly so a
        // missing first snapshot can be distinguished from a storage failure.
        Err(_) => match load_data_usage_from_backend(store).await {
            Ok(info) => info,
            Err(StorageError::ConfigNotFound) => return Ok(BucketStats::warming_up(traffic)),
            Err(error) => return Err(error),
        },
    };
    // Keep every field on the same persisted scanner snapshot. RustFS's live
    // usage overlay updates totals but not the size histogram, which would make
    // a single Stats response internally inconsistent until the next scan.
    if !info.usage_snapshot_complete {
        return Ok(BucketStats::warming_up(traffic));
    }
    let Some(usage) = info.buckets_usage.get(bucket) else {
        return Ok(BucketStats::warming_up(traffic));
    };
    Ok(BucketStats {
        ready: true,
        object_count: usage.objects_count,
        total_bytes: usage.size,
        observed_at: info.last_update.and_then(system_time_millis),
        object_size_histogram: object_size_histogram(&usage.object_size_histogram),
        traffic,
    })
}

pub async fn record_write(bucket: &str, info: &ObjectInfo, previous: Option<OldCurrentSize>) {
    let new_size = info.size.max(0) as u64;
    match previous {
        Some(OldCurrentSize::Present(size)) => {
            record_bucket_object_write_memory(bucket, Some(size.max(0) as u64), new_size).await;
        }
        Some(OldCurrentSize::Absent) => {
            record_bucket_object_write_memory(bucket, None, new_size).await;
        }
        None => {
            record_bucket_object_write_unknown_previous_memory(bucket, new_size, false).await;
        }
    }
}

pub async fn previous_size(
    store: &ECStore,
    bucket: &str,
    key: &str,
) -> Result<Option<u64>, StorageError> {
    match store
        .get_object_info(bucket, key, &ObjectOptions::default())
        .await
    {
        Ok(info) => Ok(Some(info.size.max(0) as u64)),
        Err(
            StorageError::ObjectNotFound(_, _)
            | StorageError::FileNotFound
            | StorageError::FileVersionNotFound
            | StorageError::VersionNotFound(_, _, _),
        ) => Ok(None),
        Err(error) => Err(error),
    }
}

pub async fn record_write_with_previous(bucket: &str, info: &ObjectInfo, previous: Option<u64>) {
    record_bucket_object_write_memory(bucket, previous, info.size.max(0) as u64).await;
}

pub async fn record_delete(bucket: &str, info: &ObjectInfo) {
    record_bucket_object_delete_memory(bucket, info.size.max(0) as u64, true).await;
}

// ECStore already advances scanner namespace activity for ordinary object
// mutations. Reserve an immediate dirty wakeup for bulk or maintenance changes
// whose exact per-object usage delta is unavailable, avoiding scan storms on
// write-heavy buckets.
pub fn record_dirty(bucket: &str) {
    rustfs_scanner::record_dirty_usage_bucket(bucket);
}

fn system_time_millis(value: SystemTime) -> Option<u64> {
    let millis = value
        .duration_since(SystemTime::UNIX_EPOCH)
        .ok()?
        .as_millis();
    Some(u64::try_from(millis).unwrap_or(u64::MAX))
}

fn object_size_histogram(values: &HashMap<String, u64>) -> Vec<HistogramBucket> {
    SIZE_RANGES
        .into_iter()
        .map(|(source, label)| HistogramBucket {
            label,
            count: values.get(source).copied().unwrap_or_default(),
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::object_size_histogram;
    use std::collections::HashMap;

    #[test]
    fn maps_rustfs_scanner_histogram_to_display_ranges() {
        let values = HashMap::from([
            ("LESS_THAN_1024_B".to_owned(), 4),
            ("BETWEEN_1024B_AND_1_MB".to_owned(), 3),
            ("GREATER_THAN_512_MB".to_owned(), 2),
        ]);
        let mapped = object_size_histogram(&values);
        assert_eq!(mapped.len(), 7);
        assert_eq!((mapped[0].label, mapped[0].count), ("0–1 KiB", 4));
        assert_eq!((mapped[1].label, mapped[1].count), ("1 KiB–1 MiB", 3));
        assert_eq!((mapped[6].label, mapped[6].count), ("512 MiB+", 2));
    }
}
