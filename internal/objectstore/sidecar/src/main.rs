mod bucket;
mod buffer_pool;
mod data_plane;
mod largest_objects;
mod protocol;
mod runtime;
mod s3_backend;
mod usage;

use hyper::service::service_fn;
use hyper_util::rt::TokioIo;
use runtime::StoreRuntime;
use std::{
    env,
    fs::Permissions,
    os::unix::fs::{FileTypeExt as _, PermissionsExt as _},
    path::PathBuf,
    sync::Arc,
};
use tokio::{net::UnixListener, sync::Semaphore, task::JoinSet};

const VERSION: &str = env!("CARGO_PKG_VERSION");
const MAX_CONTROL_CONNECTIONS: usize = 128;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let mut arguments = env::args().skip(1);
    if matches!(arguments.next().as_deref(), Some("--version")) {
        println!("platformd-objectstore {VERSION}");
        return Ok(());
    }
    let volume = env::var_os("PLATFORMD_OBJECTSTORE_VOLUME")
        .map(PathBuf::from)
        .ok_or("PLATFORMD_OBJECTSTORE_VOLUME is required")?;
    let socket = env::var_os("PLATFORMD_OBJECTSTORE_SOCKET")
        .map(PathBuf::from)
        .ok_or("PLATFORMD_OBJECTSTORE_SOCKET is required")?;
    if let Some(parent) = socket.parent() {
        tokio::fs::create_dir_all(parent).await?;
        tokio::fs::set_permissions(parent, Permissions::from_mode(0o700)).await?;
    }
    match tokio::fs::symlink_metadata(&socket).await {
        Ok(metadata) if metadata.file_type().is_socket() => tokio::fs::remove_file(&socket).await?,
        Ok(_) => return Err("object store control path is not a socket".into()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
        Err(error) => return Err(error.into()),
    }

    let runtime = StoreRuntime::open(&volume).await?;
    let store = runtime.store.clone();
    let data_plane = data_plane::DataPlane::new(store.clone());
    let largest_objects = largest_objects::LargestObjectSearches::default();
    let listener = UnixListener::bind(&socket)?;
    tokio::fs::set_permissions(&socket, Permissions::from_mode(0o600)).await?;

    let shutdown = shutdown_signal();
    tokio::pin!(shutdown);
    let connection_limit = Arc::new(Semaphore::new(MAX_CONTROL_CONNECTIONS));
    let mut connections = JoinSet::new();
    loop {
        tokio::select! {
            _ = &mut shutdown => break,
            completed = connections.join_next(), if !connections.is_empty() => {
                if let Some(Err(error)) = completed {
                    eprintln!("objectstore sidecar task: {error}");
                }
            }
            accepted = listener.accept() => {
                let (connection, _) = accepted?;
                let Ok(permit) = connection_limit.clone().try_acquire_owned() else {
                    drop(connection);
                    continue;
                };
                let store = store.clone();
                let data_plane = data_plane.clone();
                let largest_objects = largest_objects.clone();
                connections.spawn(async move {
                    let _permit = permit;
                    let service = service_fn(move |request| {
                        protocol::handle(
                            store.clone(),
                            data_plane.clone(),
                            largest_objects.clone(),
                            request,
                        )
                    });
                    if let Err(error) = hyper::server::conn::http1::Builder::new()
                        .serve_connection(TokioIo::new(connection), service)
                        .await
                    {
                        eprintln!("objectstore sidecar connection: {error}");
                    }
                });
            }
        }
    }
    connections.abort_all();
    while connections.join_next().await.is_some() {}
    data_plane.shutdown().await;
    drop(listener);
    let _ = tokio::fs::remove_file(&socket).await;
    runtime.close().await;
    Ok(())
}

async fn shutdown_signal() {
    #[cfg(unix)]
    {
        let mut terminate =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                .expect("install SIGTERM handler");
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {}
            _ = terminate.recv() => {}
        }
    }
    #[cfg(not(unix))]
    let _ = tokio::signal::ctrl_c().await;
}
