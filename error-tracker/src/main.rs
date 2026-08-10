use std::env;
use std::net::SocketAddr;
use std::path::PathBuf;

use anyhow::{Context, bail};
use error_tracker::{Server, ServerOptions};
use tracing_subscriber::EnvFilter;

const DEFAULT_TRACKER_NAME: &str = "Error Tracker";
const DEFAULT_LOG_FILTER: &str = "warn,error_tracker=info";

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    if env::args().nth(1).as_deref() == Some("--version") {
        println!("error-tracker {}", env!("CARGO_PKG_VERSION"));
        return Ok(());
    }
    tracing_subscriber::fmt()
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| DEFAULT_LOG_FILTER.into()),
        )
        .init();

    let admin_token = optional("ERROR_TRACKER_ADMIN_TOKEN");
    let ui_enabled = optional_bool("ERROR_TRACKER_UI_ENABLED")?.unwrap_or(admin_token.is_some());
    let volume = PathBuf::from(value("ERROR_TRACKER_VOLUME", "/data"));
    let slug = value("ERROR_TRACKER_SLUG", "error-tracker");
    let listen = value("ERROR_TRACKER_LISTEN", "0.0.0.0:8080")
        .parse::<SocketAddr>()
        .context("parse ERROR_TRACKER_LISTEN")?;
    let options = ServerOptions {
        listen,
        volume,
        public_url: value("ERROR_TRACKER_PUBLIC_URL", "http://localhost:8080"),
        name: DEFAULT_TRACKER_NAME.into(),
        slug,
        admin_token,
        ui_enabled,
        endpoint_file: optional("ERROR_TRACKER_ENDPOINT_FILE").map(PathBuf::from),
    };
    Server::new(options).await?.serve().await
}

fn value(name: &str, default: &str) -> String {
    env::var(name).unwrap_or_else(|_| default.to_owned())
}

fn optional(name: &str) -> Option<String> {
    env::var(name).ok().filter(|value| !value.is_empty())
}

fn optional_bool(name: &str) -> anyhow::Result<Option<bool>> {
    let Some(value) = optional(name) else {
        return Ok(None);
    };
    match value.to_ascii_lowercase().as_str() {
        "true" | "1" => Ok(Some(true)),
        "false" | "0" => Ok(Some(false)),
        _ => bail!("{name} must be true, false, 1, or 0"),
    }
}
