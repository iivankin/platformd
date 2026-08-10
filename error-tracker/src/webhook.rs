use std::time::Duration;

use hmac::{Hmac, Mac};
use reqwest::Client;
use serde::Serialize;
use sha2::Sha256;
use tokio::sync::mpsc::{self, error::TrySendError};
use tokio::task::JoinSet;
use tracing::warn;
use uuid::Uuid;

use crate::config::{AppConfig, TrackerConfig, WebhookConfig, WebhookEvent};
use crate::ingest::IngestedEvent;

type HmacSha256 = Hmac<Sha256>;

#[derive(Clone)]
pub struct Dispatcher {
    sender: mpsc::Sender<Delivery>,
}

struct Delivery {
    webhook: WebhookConfig,
    payload: Payload,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct Payload {
    id: String,
    event_type: WebhookEvent,
    timestamp: String,
    tracker: WebhookTracker,
    app: WebhookApp,
    issue: WebhookIssue,
    event: WebhookOccurrence,
}

#[derive(Clone, Serialize)]
struct WebhookTracker {
    name: String,
    slug: String,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct WebhookApp {
    id: String,
    project_id: String,
    name: String,
    slug: String,
}

#[derive(Clone, Serialize)]
struct WebhookIssue {
    id: String,
    title: String,
    level: String,
    platform: String,
}

#[derive(Clone, Serialize)]
struct WebhookOccurrence {
    id: String,
    timestamp: String,
}

impl Dispatcher {
    pub fn new() -> Result<Self, reqwest::Error> {
        let client = Client::builder()
            .connect_timeout(Duration::from_secs(5))
            .timeout(Duration::from_secs(10))
            .no_proxy()
            .build()?;
        let (sender, receiver) = mpsc::channel(256);
        tokio::spawn(run_dispatcher(client, receiver));
        Ok(Self { sender })
    }

    pub fn dispatch(
        &self,
        tracker: &TrackerConfig,
        app: &AppConfig,
        event: &IngestedEvent,
        is_new_issue: bool,
        is_regressed: bool,
    ) {
        self.dispatch_type(tracker, app, event, WebhookEvent::EventReceived);
        if is_new_issue {
            self.dispatch_type(tracker, app, event, WebhookEvent::IssueCreated);
        }
        if is_regressed {
            self.dispatch_type(tracker, app, event, WebhookEvent::IssueRegressed);
        }
    }

    pub fn dispatch_state(
        &self,
        tracker: &TrackerConfig,
        app: &AppConfig,
        event: &IngestedEvent,
        event_type: WebhookEvent,
    ) {
        self.dispatch_type(tracker, app, event, event_type);
    }

    fn dispatch_type(
        &self,
        tracker: &TrackerConfig,
        app: &AppConfig,
        event: &IngestedEvent,
        event_type: WebhookEvent,
    ) {
        for webhook in app
            .webhooks
            .iter()
            .filter(|webhook| webhook.enabled && webhook.events.contains(&event_type))
        {
            let webhook = webhook.clone();
            let payload = Payload {
                id: Uuid::new_v4().simple().to_string(),
                event_type,
                timestamp: event.timestamp.clone(),
                tracker: WebhookTracker {
                    name: tracker.name.clone(),
                    slug: tracker.slug.clone(),
                },
                app: WebhookApp {
                    id: app.id.clone(),
                    project_id: app.project_id.clone(),
                    name: app.name.clone(),
                    slug: app.slug.clone(),
                },
                issue: WebhookIssue {
                    id: event.issue_id.clone(),
                    title: event.title.clone(),
                    level: event.level.clone(),
                    platform: event.platform.clone(),
                },
                event: WebhookOccurrence {
                    id: event.event_id.clone(),
                    timestamp: event.timestamp.clone(),
                },
            };
            if let Err(error) = self.sender.try_send(Delivery { webhook, payload }) {
                match error {
                    TrySendError::Full(delivery) => warn!(
                        webhook_id = delivery.webhook.id,
                        "webhook queue is full; delivery dropped"
                    ),
                    TrySendError::Closed(delivery) => warn!(
                        webhook_id = delivery.webhook.id,
                        "webhook dispatcher stopped"
                    ),
                }
            }
        }
    }
}

async fn run_dispatcher(client: Client, mut receiver: mpsc::Receiver<Delivery>) {
    const MAX_IN_FLIGHT: usize = 4;

    let mut active = JoinSet::new();
    loop {
        if active.len() == MAX_IN_FLIGHT {
            log_worker_result(active.join_next().await);
            continue;
        }
        if active.is_empty() {
            let Some(delivery) = receiver.recv().await else {
                break;
            };
            spawn_delivery(&mut active, &client, delivery);
            continue;
        }
        tokio::select! {
            delivery = receiver.recv() => match delivery {
                Some(delivery) => spawn_delivery(&mut active, &client, delivery),
                None => break,
            },
            result = active.join_next() => log_worker_result(result),
        }
    }
    while !active.is_empty() {
        log_worker_result(active.join_next().await);
    }
}

fn spawn_delivery(active: &mut JoinSet<()>, client: &Client, delivery: Delivery) {
    let client = client.clone();
    active.spawn(async move {
        if let Err(error) = deliver(&client, &delivery.webhook, &delivery.payload).await {
            warn!(webhook_id = delivery.webhook.id, %error, "webhook delivery failed");
        }
    });
}

fn log_worker_result(result: Option<Result<(), tokio::task::JoinError>>) {
    if let Some(Err(error)) = result {
        warn!(%error, "webhook delivery task failed");
    }
}

async fn deliver(
    client: &Client,
    webhook: &WebhookConfig,
    payload: &Payload,
) -> Result<(), String> {
    let body = serde_json::to_vec(payload).map_err(|error| error.to_string())?;
    let mut mac =
        HmacSha256::new_from_slice(webhook.secret.as_bytes()).map_err(|error| error.to_string())?;
    mac.update(&body);
    let signature = format!("sha256={}", hex_bytes(&mac.finalize().into_bytes()));
    let event_name = match payload.event_type {
        WebhookEvent::IssueCreated => "issue_created",
        WebhookEvent::IssueRegressed => "issue_regressed",
        WebhookEvent::IssueResolved => "issue_resolved",
        WebhookEvent::EventReceived => "event_received",
    };
    let delays = [
        Duration::ZERO,
        Duration::from_secs(1),
        Duration::from_secs(3),
        Duration::from_secs(9),
    ];
    let mut last_error = String::new();
    for delay in delays {
        if !delay.is_zero() {
            tokio::time::sleep(delay).await;
        }
        let response = client
            .post(&webhook.url)
            .header("content-type", "application/json")
            .header("user-agent", "error-tracker-webhooks/1")
            .header("x-error-tracker-delivery", &payload.id)
            .header("x-error-tracker-event", event_name)
            .header("x-error-tracker-signature", &signature)
            .body(body.clone())
            .send()
            .await;
        match response {
            Ok(response) if response.status().is_success() => return Ok(()),
            Ok(response)
                if response.status().is_server_error()
                    || response.status().as_u16() == 408
                    || response.status().as_u16() == 425
                    || response.status().as_u16() == 429 =>
            {
                last_error = format!("HTTP {}", response.status());
            }
            Ok(response) => return Err(format!("HTTP {}", response.status())),
            Err(error) => last_error = error.to_string(),
        }
    }
    Err(last_error)
}

fn hex_bytes(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut output = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        output.push(HEX[(byte >> 4) as usize] as char);
        output.push(HEX[(byte & 0x0f) as usize] as char);
    }
    output
}
