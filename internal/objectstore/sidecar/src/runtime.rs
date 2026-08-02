use rustfs_ecstore::api::{
    bucket::metadata_sys::init_bucket_metadata_sys,
    config::{init as init_ecstore_config, init_global_config_sys},
    layout::EndpointServerPools,
    runtime::InstanceContext,
    storage::{
        ECStore, init_local_disks_with_instance_ctx, init_lock_clients,
        prewarm_local_disk_id_map_with_instance_ctx,
    },
};
use rustfs_heal::{
    create_ahm_services_cancel_token,
    heal::{clear_unclean_shutdown_markers, storage::ECStoreHealStorage},
    init_heal_manager, shutdown_ahm_services,
};
use rustfs_storage_api::{BucketOperations as _, BucketOptions};
use std::{
    fs, io,
    net::SocketAddr,
    path::{Component, Path},
    sync::Arc,
};
use tokio_util::sync::CancellationToken;

pub struct StoreRuntime {
    pub store: Arc<ECStore>,
    shutdown: CancellationToken,
}

impl StoreRuntime {
    pub async fn open(volume: &Path) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        prepare_volume(volume)?;
        let address: SocketAddr = "127.0.0.1:0".parse()?;
        let (endpoint_pools, setup_type) = EndpointServerPools::from_volumes(
            &address.to_string(),
            vec![volume.to_string_lossy().into_owned()],
        )
        .await?;
        ECStore::validate_startup_storage_class(&endpoint_pools)?;

        let instance = Arc::new(InstanceContext::new());
        instance.set_endpoints(endpoint_pools.clone());
        instance.update_erasure_type(setup_type).await;
        init_local_disks_with_instance_ctx(&instance, endpoint_pools.clone()).await?;
        prewarm_local_disk_id_map_with_instance_ctx(&instance).await;
        init_lock_clients(endpoint_pools.clone());

        let shutdown = CancellationToken::new();
        let store =
            ECStore::new_with_instance_ctx(address, endpoint_pools, shutdown.clone(), instance)
                .await?;

        init_ecstore_config();
        init_global_config_sys(store.clone()).await?;
        let buckets = store
            .list_bucket(&BucketOptions {
                no_metadata: true,
                ..Default::default()
            })
            .await?
            .into_iter()
            .map(|bucket| bucket.name)
            .collect();
        init_bucket_metadata_sys(store.clone(), buckets).await;

        let _heal_shutdown = create_ahm_services_cancel_token();
        init_heal_manager(Arc::new(ECStoreHealStorage::new(store.clone())), None).await?;
        Ok(Self { store, shutdown })
    }

    pub async fn close(self) {
        self.shutdown.cancel();
        rustfs_ecstore::shutdown_background_monitors();
        rustfs_ecstore::api::global::shutdown_background_services();
        shutdown_ahm_services();
        clear_unclean_shutdown_markers().await;
        drop(self.store);
    }
}

fn prepare_volume(volume: &Path) -> io::Result<()> {
    if !volume.is_absolute()
        || volume.file_name().and_then(|name| name.to_str()) != Some("objects")
        || volume
            .components()
            .any(|component| matches!(component, Component::CurDir | Component::ParentDir))
    {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "object store volume must be an absolute normalized path named objects",
        ));
    }
    let parent = volume.parent().ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            "object store volume has no parent",
        )
    })?;
    fs::create_dir_all(parent)?;
    match fs::symlink_metadata(volume) {
        Ok(metadata) if metadata.is_dir() && !metadata.file_type().is_symlink() => Ok(()),
        Ok(_) => Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "object store volume is not a real directory",
        )),
        Err(error) if error.kind() == io::ErrorKind::NotFound => fs::create_dir(volume),
        Err(error) => Err(error),
    }
}

#[cfg(test)]
mod tests {
    use super::prepare_volume;

    #[test]
    fn prepare_volume_preserves_existing_rustfs_data() {
        let root = tempfile::tempdir().expect("tempdir");
        let volume = root.path().join("objects");
        prepare_volume(&volume).expect("create volume");
        std::fs::write(volume.join("rustfs-data"), b"current").expect("current data");

        prepare_volume(&volume).expect("reopen volume");
        assert_eq!(
            std::fs::read(volume.join("rustfs-data")).expect("preserved data"),
            b"current"
        );
    }

    #[test]
    fn rejects_broad_or_unexpected_paths() {
        for path in ["/var/lib", "/var/lib/platformd/data"] {
            let error = prepare_volume(std::path::Path::new(path)).expect_err("unsafe path");
            assert_eq!(error.kind(), std::io::ErrorKind::InvalidInput);
        }
    }
}
