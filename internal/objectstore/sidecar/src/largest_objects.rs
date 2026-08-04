use rustfs_ecstore::api::{object::ObjectInfo, storage::ECStore};
use rustfs_storage_api::ListOperations as _;
use serde::Serialize;
use std::{collections::HashMap, sync::Arc};
use tokio::sync::Mutex;
use tokio_util::sync::CancellationToken;

const PAGE_SIZE: i32 = 1000;
const RESULT_LIMIT: usize = 10;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum SearchStatus {
    Idle,
    Running,
    Cancelling,
    Complete,
    Cancelled,
    Failed,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LargestObject {
    pub key: String,
    pub size: u64,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LargestObjectsSearch {
    pub status: SearchStatus,
    pub scanned_objects: u64,
    pub objects: Vec<LargestObject>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

impl LargestObjectsSearch {
    fn idle() -> Self {
        Self {
            status: SearchStatus::Idle,
            scanned_objects: 0,
            objects: Vec::new(),
            error: None,
        }
    }

    fn running() -> Self {
        Self {
            status: SearchStatus::Running,
            ..Self::idle()
        }
    }
}

struct SearchJob {
    id: u64,
    state: LargestObjectsSearch,
    cancel: Option<CancellationToken>,
}

#[derive(Default)]
struct SearchRegistry {
    next_id: u64,
    jobs: HashMap<String, SearchJob>,
}

#[derive(Clone, Default)]
pub struct LargestObjectSearches {
    inner: Arc<Mutex<SearchRegistry>>,
}

impl LargestObjectSearches {
    pub async fn status(&self, bucket: &str) -> LargestObjectsSearch {
        self.inner
            .lock()
            .await
            .jobs
            .get(bucket)
            .map(|job| job.state.clone())
            .unwrap_or_else(LargestObjectsSearch::idle)
    }

    pub async fn start(&self, store: Arc<ECStore>, bucket: String) -> LargestObjectsSearch {
        let (id, cancel, state) = {
            let mut registry = self.inner.lock().await;
            if let Some(job) = registry.jobs.get(&bucket)
                && matches!(
                    job.state.status,
                    SearchStatus::Running | SearchStatus::Cancelling
                )
            {
                return job.state.clone();
            }

            registry.next_id = registry
                .next_id
                .checked_add(1)
                .expect("largest object search ID overflow");
            let id = registry.next_id;
            let cancel = CancellationToken::new();
            let state = LargestObjectsSearch::running();
            registry.jobs.insert(
                bucket.clone(),
                SearchJob {
                    id,
                    state: state.clone(),
                    cancel: Some(cancel.clone()),
                },
            );
            (id, cancel, state)
        };

        let searches = self.clone();
        tokio::spawn(async move {
            let outcome = scan_bucket(searches.clone(), store, &bucket, id, cancel).await;
            searches.finish(&bucket, id, outcome).await;
        });
        state
    }

    pub async fn cancel(&self, bucket: &str) -> LargestObjectsSearch {
        let (state, cancel) = {
            let mut registry = self.inner.lock().await;
            let Some(job) = registry.jobs.get_mut(bucket) else {
                return LargestObjectsSearch::idle();
            };
            if job.state.status == SearchStatus::Running {
                job.state.status = SearchStatus::Cancelling;
            }
            (job.state.clone(), job.cancel.clone())
        };
        if state.status == SearchStatus::Cancelling
            && let Some(cancel) = cancel
        {
            cancel.cancel();
        }
        state
    }

    async fn record_progress(&self, bucket: &str, id: u64, scanned_objects: u64) -> bool {
        let mut registry = self.inner.lock().await;
        let Some(job) = registry.jobs.get_mut(bucket) else {
            return false;
        };
        if job.id != id || job.state.status != SearchStatus::Running {
            return false;
        }
        job.state.scanned_objects = scanned_objects;
        true
    }

    async fn finish(&self, bucket: &str, id: u64, outcome: SearchOutcome) {
        let mut registry = self.inner.lock().await;
        let Some(job) = registry.jobs.get_mut(bucket) else {
            return;
        };
        if job.id != id {
            return;
        }
        job.cancel = None;
        match outcome {
            SearchOutcome::Complete {
                scanned_objects,
                objects,
            } => {
                job.state.status = SearchStatus::Complete;
                job.state.scanned_objects = scanned_objects;
                job.state.objects = objects;
                job.state.error = None;
            }
            SearchOutcome::Cancelled => {
                job.state.status = SearchStatus::Cancelled;
                job.state.objects.clear();
                job.state.error = None;
            }
            SearchOutcome::Failed(error) => {
                job.state.status = SearchStatus::Failed;
                job.state.objects.clear();
                job.state.error = Some(error);
            }
        }
    }
}

enum SearchOutcome {
    Complete {
        scanned_objects: u64,
        objects: Vec<LargestObject>,
    },
    Cancelled,
    Failed(String),
}

async fn scan_bucket(
    searches: LargestObjectSearches,
    store: Arc<ECStore>,
    bucket: &str,
    id: u64,
    cancel: CancellationToken,
) -> SearchOutcome {
    let mut continuation = None;
    let mut scanned_objects = 0_u64;
    let mut largest = TopObjects::default();

    loop {
        let listed = tokio::select! {
            biased;
            _ = cancel.cancelled() => return SearchOutcome::Cancelled,
            result = Arc::clone(&store).list_objects_v2(
                bucket,
                "",
                continuation,
                None,
                PAGE_SIZE,
                false,
                None,
                false,
            ) => match result {
                Ok(listed) => listed,
                Err(error) => return SearchOutcome::Failed(error.to_string()),
            },
        };

        scanned_objects = scanned_objects
            .checked_add(
                u64::try_from(listed.objects.len()).expect("object page length exceeds u64"),
            )
            .expect("object scan count overflow");
        for object in &listed.objects {
            largest.consider(object_record(object));
        }
        if cancel.is_cancelled() || !searches.record_progress(bucket, id, scanned_objects).await {
            return SearchOutcome::Cancelled;
        }
        if !listed.is_truncated {
            return SearchOutcome::Complete {
                scanned_objects,
                objects: largest.finish(),
            };
        }
        let Some(next) = listed.next_continuation_token else {
            return SearchOutcome::Failed(
                "truncated object listing omitted its continuation token".to_owned(),
            );
        };
        continuation = Some(next);
    }
}

#[derive(Default)]
struct TopObjects {
    objects: Vec<LargestObject>,
}

impl TopObjects {
    fn consider(&mut self, candidate: LargestObject) {
        if self.objects.len() < RESULT_LIMIT {
            self.objects.push(candidate);
            return;
        }
        let mut worst = 0;
        for index in 1..self.objects.len() {
            if better(&self.objects[worst], &self.objects[index]) {
                worst = index;
            }
        }
        if better(&candidate, &self.objects[worst]) {
            self.objects[worst] = candidate;
        }
    }

    fn finish(mut self) -> Vec<LargestObject> {
        self.objects.sort_by(|left, right| {
            right
                .size
                .cmp(&left.size)
                .then_with(|| left.key.cmp(&right.key))
        });
        self.objects
    }
}

fn better(left: &LargestObject, right: &LargestObject) -> bool {
    left.size > right.size || (left.size == right.size && left.key < right.key)
}

fn object_record(info: &ObjectInfo) -> LargestObject {
    LargestObject {
        key: info.name.clone(),
        size: info.size.max(0) as u64,
    }
}

#[cfg(test)]
mod tests {
    use super::{LargestObject, TopObjects};

    #[test]
    fn retains_ten_largest_objects_with_stable_ties() {
        let mut largest = TopObjects::default();
        for (key, size) in [
            ("m", 5),
            ("z", 10),
            ("a", 10),
            ("b", 3),
            ("c", 8),
            ("d", 7),
            ("e", 6),
            ("f", 4),
            ("g", 2),
            ("h", 1),
            ("i", 9),
            ("j", 11),
            ("k", 12),
        ] {
            largest.consider(LargestObject {
                key: key.to_owned(),
                size,
            });
        }

        let result = largest.finish();
        assert_eq!(result.len(), 10);
        assert_eq!(result[0].key, "k");
        assert_eq!(result[1].key, "j");
        assert_eq!(result[2].key, "a");
        assert_eq!(result[3].key, "z");
        assert_eq!(result.last().map(|object| object.key.as_str()), Some("f"));
    }
}
