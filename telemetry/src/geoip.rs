use std::fs::File;
use std::io::{self, ErrorKind, Read};
use std::net::IpAddr;
use std::path::Path;
use std::time::Duration;

use flate2::read::GzDecoder;
use jiff::{ToSpan, Zoned, civil::Date};
use maxminddb::{MaxMindDbError, Reader, geoip2};
use serde_json::{Map, Value};
use tokio::io::AsyncWriteExt;

pub(crate) const DATABASE_FILENAME: &str = "GeoIP-City.mmdb";
const DOWNLOAD_ARCHIVE_FILENAME: &str = ".GeoIP-City.mmdb.gz.download";
const DOWNLOAD_DATABASE_FILENAME: &str = ".GeoIP-City.mmdb.download";
const MAX_ARCHIVE_BYTES: u64 = 256 << 20;
const MAX_DATABASE_BYTES: u64 = 512 << 20;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum GeoIpSource {
    Cloudflare,
    Database,
}

pub(crate) struct GeoIpLookup {
    reader: Option<Reader<Vec<u8>>>,
    source: GeoIpSource,
}

impl GeoIpLookup {
    pub(crate) async fn from_volume(volume: &Path, source: GeoIpSource) -> Self {
        if source == GeoIpSource::Cloudflare {
            tracing::info!("GeoIP uses Cloudflare request headers");
            return Self {
                reader: None,
                source,
            };
        }

        let path = volume.join(DATABASE_FILENAME);
        if let Some(reader) = load_database(&path) {
            return Self {
                reader: Some(reader),
                source,
            };
        }
        if let Err(error) = download_database(volume).await {
            tracing::warn!(
                %error,
                path = %path.display(),
                "GeoIP database download failed; location enrichment is disabled"
            );
            return Self {
                reader: None,
                source,
            };
        }
        Self {
            reader: load_database(&path),
            source,
        }
    }

    #[cfg(test)]
    pub(crate) const fn empty() -> Self {
        Self {
            reader: None,
            source: GeoIpSource::Database,
        }
    }

    pub(crate) fn request_geo(&self, cloudflare_country: Option<&str>) -> Option<Geo> {
        (self.source == GeoIpSource::Cloudflare)
            .then(|| cloudflare_country.and_then(Geo::from_cloudflare_country))
            .flatten()
    }

    pub(crate) fn lookup(&self, address: IpAddr) -> Option<Geo> {
        let city = self
            .reader
            .as_ref()?
            .lookup(address)
            .ok()?
            .decode::<geoip2::City>()
            .ok()??;
        let geo = Geo {
            country_code: city.country.iso_code.map(str::to_owned),
            city: city.city.names.english.map(str::to_owned),
            subdivision: city
                .subdivisions
                .first()
                .and_then(|subdivision| subdivision.names.english)
                .map(str::to_owned),
            region: city.country.names.english.map(str::to_owned),
            source: Some("https://db-ip.com".into()),
        };
        geo.has_values().then_some(geo)
    }
}

fn load_database(path: &Path) -> Option<Reader<Vec<u8>>> {
    match Reader::open_readfile(path) {
        Ok(reader) => {
            tracing::info!(path = %path.display(), "GeoIP database loaded");
            Some(reader)
        }
        Err(MaxMindDbError::Io(error)) if error.kind() == ErrorKind::NotFound => None,
        Err(error) => {
            tracing::warn!(
                %error,
                path = %path.display(),
                "GeoIP database is invalid; downloading a replacement"
            );
            None
        }
    }
}

async fn download_database(volume: &Path) -> Result<(), String> {
    let archive = volume.join(DOWNLOAD_ARCHIVE_FILENAME);
    let temporary = volume.join(DOWNLOAD_DATABASE_FILENAME);
    let database = volume.join(DATABASE_FILENAME);
    let client = reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(5 * 60))
        .build()
        .map_err(|error| format!("build GeoIP download client: {error}"))?;
    let mut last_error = None;
    for url in database_urls(Zoned::now().date()) {
        let _ = tokio::fs::remove_file(&archive).await;
        match download_archive(&client, &url, &archive).await {
            Ok(()) => {
                let archive_for_unpack = archive.clone();
                let temporary_for_unpack = temporary.clone();
                let database_for_unpack = database.clone();
                let unpacked = tokio::task::spawn_blocking(move || {
                    unpack_database(
                        &archive_for_unpack,
                        &temporary_for_unpack,
                        &database_for_unpack,
                    )
                })
                .await
                .map_err(|error| format!("join GeoIP decompression task: {error}"))?;
                let _ = tokio::fs::remove_file(&archive).await;
                if let Err(error) = unpacked {
                    let _ = tokio::fs::remove_file(&temporary).await;
                    return Err(error);
                }
                tracing::info!(path = %database.display(), source = %url, "GeoIP database downloaded");
                return Ok(());
            }
            Err(error) => last_error = Some(error),
        }
    }
    let _ = tokio::fs::remove_file(&archive).await;
    Err(last_error.unwrap_or_else(|| "no GeoIP database download URL was available".into()))
}

async fn download_archive(
    client: &reqwest::Client,
    url: &str,
    destination: &Path,
) -> Result<(), String> {
    let mut response = client
        .get(url)
        .send()
        .await
        .and_then(reqwest::Response::error_for_status)
        .map_err(|error| format!("download {url}: {error}"))?;
    if response
        .content_length()
        .is_some_and(|length| length > MAX_ARCHIVE_BYTES)
    {
        return Err(format!("download {url}: archive exceeds size limit"));
    }
    let mut file = tokio::fs::File::create(destination)
        .await
        .map_err(|error| format!("create {}: {error}", destination.display()))?;
    let mut downloaded = 0_u64;
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|error| format!("read {url}: {error}"))?
    {
        downloaded = downloaded
            .checked_add(chunk.len() as u64)
            .ok_or_else(|| format!("download {url}: archive size overflow"))?;
        if downloaded > MAX_ARCHIVE_BYTES {
            return Err(format!("download {url}: archive exceeds size limit"));
        }
        file.write_all(&chunk)
            .await
            .map_err(|error| format!("write {}: {error}", destination.display()))?;
    }
    file.sync_all()
        .await
        .map_err(|error| format!("sync {}: {error}", destination.display()))
}

fn unpack_database(archive: &Path, temporary: &Path, destination: &Path) -> Result<(), String> {
    let archive = File::open(archive).map_err(|error| format!("open GeoIP archive: {error}"))?;
    let mut input = GzDecoder::new(archive).take(MAX_DATABASE_BYTES + 1);
    let mut output = File::create(temporary)
        .map_err(|error| format!("create {}: {error}", temporary.display()))?;
    let written = io::copy(&mut input, &mut output)
        .map_err(|error| format!("decompress GeoIP database: {error}"))?;
    if written > MAX_DATABASE_BYTES {
        return Err("decompressed GeoIP database exceeds size limit".into());
    }
    output
        .sync_all()
        .map_err(|error| format!("sync {}: {error}", temporary.display()))?;
    drop(output);
    Reader::open_readfile(temporary)
        .map_err(|error| format!("validate downloaded GeoIP database: {error}"))?;
    std::fs::rename(temporary, destination)
        .map_err(|error| format!("install {}: {error}", destination.display()))
}

fn database_urls(date: Date) -> [String; 2] {
    let previous = date
        .checked_sub(1.months())
        .expect("the current date always has a previous month");
    [database_url(date), database_url(previous)]
}

fn database_url(date: Date) -> String {
    format!(
        "https://download.db-ip.com/free/dbip-city-lite-{:04}-{:02}.mmdb.gz",
        date.year(),
        date.month()
    )
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct Geo {
    country_code: Option<String>,
    city: Option<String>,
    subdivision: Option<String>,
    region: Option<String>,
    source: Option<String>,
}

impl Geo {
    fn from_cloudflare_country(value: &str) -> Option<Self> {
        let country_code = value.trim().to_ascii_uppercase();
        if country_code.len() != 2
            || !country_code.bytes().all(|byte| byte.is_ascii_alphabetic())
            || matches!(country_code.as_str(), "XX" | "T1")
        {
            return None;
        }
        Some(Self {
            country_code: Some(country_code),
            source: Some("Cloudflare".into()),
            ..Self::default()
        })
    }

    fn has_values(&self) -> bool {
        self.country_code.is_some()
            || self.city.is_some()
            || self.subdivision.is_some()
            || self.region.is_some()
    }

    pub(crate) fn into_value(self) -> Value {
        let mut value = Map::new();
        insert(&mut value, "country_code", self.country_code);
        insert(&mut value, "city", self.city);
        insert(&mut value, "subdivision", self.subdivision);
        insert(&mut value, "region", self.region);
        insert(&mut value, "source", self.source);
        Value::Object(value)
    }

    #[cfg(test)]
    pub(crate) fn fixture() -> Self {
        Self {
            country_code: Some("GB".into()),
            city: Some("Boxford".into()),
            subdivision: Some("England".into()),
            region: Some("United Kingdom".into()),
            source: None,
        }
    }
}

fn insert(value: &mut Map<String, Value>, key: &str, field: Option<String>) {
    if let Some(field) = field {
        value.insert(key.into(), Value::String(field));
    }
}

#[cfg(test)]
mod tests {
    use tempfile::tempdir;

    use super::*;

    #[tokio::test]
    async fn cloudflare_source_skips_database_and_validates_country() {
        let volume = tempdir().unwrap();
        let lookup = GeoIpLookup::from_volume(volume.path(), GeoIpSource::Cloudflare).await;

        assert_eq!(
            lookup.request_geo(Some("rs")).map(Geo::into_value),
            Some(serde_json::json!({
                "country_code": "RS",
                "source": "Cloudflare"
            }))
        );
        assert!(lookup.request_geo(Some("XX")).is_none());
        assert!(lookup.request_geo(Some("T1")).is_none());
        assert!(lookup.request_geo(Some("invalid")).is_none());
        assert!(!volume.path().join(DATABASE_FILENAME).exists());
    }

    #[test]
    fn database_download_uses_current_and_previous_month() {
        let urls = database_urls("2026-01-15".parse().unwrap());

        assert_eq!(
            urls,
            [
                "https://download.db-ip.com/free/dbip-city-lite-2026-01.mmdb.gz",
                "https://download.db-ip.com/free/dbip-city-lite-2025-12.mmdb.gz",
            ]
        );
    }

    #[test]
    fn invalid_database_disables_lookup() {
        let volume = tempdir().unwrap();
        std::fs::write(volume.path().join(DATABASE_FILENAME), b"not a database").unwrap();

        assert!(load_database(&volume.path().join(DATABASE_FILENAME)).is_none());
    }
}
