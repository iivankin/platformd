mod artifact;
mod auth;
mod envelope;
pub mod error;
mod gen_ai;
mod geoip;
mod ingest;
mod metric_sql;
mod model;
mod server;
mod service;
mod storage;
mod symbolicator;
mod telemetry;

pub(crate) const MAX_STORED_ITEM_BYTES: usize = 200 << 20;

pub use geoip::GeoIpSource;
pub use server::{TelemetryOptions, TelemetryServer};

pub async fn restore_backup(
    archive: std::path::PathBuf,
    volume: std::path::PathBuf,
) -> error::Result<()> {
    storage::Store::restore_backup(archive, volume).await
}
