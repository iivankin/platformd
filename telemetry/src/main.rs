use std::env;
use std::net::SocketAddr;
use std::path::PathBuf;

use anyhow::{Context, bail};
use platformd_telemetry::{GeoIpSource, TelemetryOptions, TelemetryServer};
use tracing_subscriber::EnvFilter;

const DEFAULT_LOG_FILTER: &str = "warn,platformd_telemetry=info";

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let mut arguments = env::args_os().skip(1);
    match arguments.next().as_deref().and_then(|value| value.to_str()) {
        Some("--version") if arguments.next().is_none() => {
            println!("platformd-telemetry {}", env!("CARGO_PKG_VERSION"));
            return Ok(());
        }
        Some("--restore-backup") => {
            let archive = arguments
                .next()
                .map(PathBuf::from)
                .context("missing telemetry backup archive")?;
            let volume = arguments
                .next()
                .map(PathBuf::from)
                .context("missing telemetry restore volume")?;
            if arguments.next().is_some() {
                bail!("unexpected telemetry restore argument");
            }
            platformd_telemetry::restore_backup(archive, volume).await?;
            return Ok(());
        }
        Some(_) => bail!("unknown argument"),
        None => {}
    }
    tracing_subscriber::fmt()
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| DEFAULT_LOG_FILTER.into()),
        )
        .init();

    let volume = PathBuf::from(value("PLATFORMD_TELEMETRY_VOLUME", "/data"));
    let listen = value("PLATFORMD_TELEMETRY_SENTRY_LISTEN", "127.0.0.1:4319")
        .parse::<SocketAddr>()
        .context("parse PLATFORMD_TELEMETRY_SENTRY_LISTEN")?;
    let otlp_grpc_listen = value("PLATFORMD_TELEMETRY_OTLP_GRPC_LISTEN", "127.0.0.1:4317")
        .parse::<SocketAddr>()
        .context("parse PLATFORMD_TELEMETRY_OTLP_GRPC_LISTEN")?;
    let otlp_http_listen = value("PLATFORMD_TELEMETRY_OTLP_HTTP_LISTEN", "127.0.0.1:4318")
        .parse::<SocketAddr>()
        .context("parse PLATFORMD_TELEMETRY_OTLP_HTTP_LISTEN")?;
    let geoip_source = match value("PLATFORMD_TELEMETRY_GEOIP_SOURCE", "database")
        .to_ascii_lowercase()
        .as_str()
    {
        "cloudflare" => GeoIpSource::Cloudflare,
        "database" => GeoIpSource::Database,
        _ => bail!("PLATFORMD_TELEMETRY_GEOIP_SOURCE must be cloudflare or database"),
    };
    let options = TelemetryOptions {
        listen,
        otlp_grpc_listen,
        otlp_http_listen,
        volume,
        geoip_source,
    };
    let server = TelemetryServer::new(options).await?;
    tokio::select! {
        result = server.serve() => result,
        result = shutdown_signal() => result,
    }
}

#[cfg(unix)]
async fn shutdown_signal() -> anyhow::Result<()> {
    let mut terminate = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())?;
    tokio::select! {
        result = tokio::signal::ctrl_c() => result?,
        _ = terminate.recv() => {},
    }
    Ok(())
}

#[cfg(not(unix))]
async fn shutdown_signal() -> anyhow::Result<()> {
    tokio::signal::ctrl_c().await?;
    Ok(())
}

fn value(name: &str, default: &str) -> String {
    env::var(name).unwrap_or_else(|_| default.to_owned())
}
