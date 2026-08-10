use std::num::NonZeroU64;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};
use tokio::io::AsyncWriteExt;
use tokio::sync::{Mutex, RwLock};
use url::Url;
use uuid::Uuid;

use crate::auth::{token_hash, token_hash_matches};
use crate::error::{Error, Result};

const CONFIG_VERSION: u32 = 5;

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct TrackerConfig {
    pub version: u32,
    pub name: String,
    pub slug: String,
    pub apps: Vec<AppConfig>,
    pub api_tokens: Vec<ApiTokenConfig>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AppConfig {
    pub id: String,
    pub project_id: String,
    pub name: String,
    pub slug: String,
    pub public_key: String,
    pub auth_token_hash: String,
    #[serde(default)]
    pub webhooks: Vec<WebhookConfig>,
    pub created_at: String,
    pub updated_at: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct WebhookConfig {
    pub id: String,
    pub url: String,
    pub secret: String,
    pub events: Vec<WebhookEvent>,
    pub enabled: bool,
    pub created_at: String,
    pub updated_at: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ApiTokenConfig {
    pub id: String,
    pub name: String,
    pub role: ApiTokenRole,
    pub app_id: Option<String>,
    secret_hash: String,
    pub created_at: String,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum ApiTokenRole {
    Read,
    Admin,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum WebhookEvent {
    IssueCreated,
    IssueRegressed,
    IssueResolved,
    EventReceived,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CreateApp {
    pub name: String,
    pub slug: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CreateWebhook {
    pub url: String,
    pub events: Vec<WebhookEvent>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct UpdateWebhook {
    pub url: String,
    pub events: Vec<WebhookEvent>,
    pub enabled: bool,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CreateApiToken {
    pub name: String,
    pub role: ApiTokenRole,
    pub app_id: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CreatedWebhook {
    #[serde(flatten)]
    pub webhook: WebhookView,
    pub secret: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CreatedApp {
    #[serde(flatten)]
    pub app: AppView,
    pub auth_token: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CreatedApiToken {
    #[serde(flatten)]
    pub api_token: ApiTokenView,
    pub token: String,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AppView {
    pub id: String,
    pub project_id: String,
    pub name: String,
    pub slug: String,
    pub public_key: String,
    pub dsn: String,
    pub webhooks: Vec<WebhookView>,
    pub created_at: String,
    pub updated_at: String,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct WebhookView {
    pub id: String,
    pub url: String,
    pub events: Vec<WebhookEvent>,
    pub enabled: bool,
    pub created_at: String,
    pub updated_at: String,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ApiTokenView {
    pub id: String,
    pub name: String,
    pub role: ApiTokenRole,
    pub app_id: Option<String>,
    pub created_at: String,
}

pub struct ConfigRepository {
    path: PathBuf,
    public_url: String,
    current: RwLock<TrackerConfig>,
    mutation: Mutex<()>,
}

impl ConfigRepository {
    pub async fn open(
        path: PathBuf,
        name: String,
        slug: String,
        public_url: String,
    ) -> Result<Self> {
        let public_url = normalized_public_url(&public_url)?;
        let config = match tokio::fs::read(&path).await {
            Ok(bytes) => serde_json::from_slice(&bytes).map_err(|error| {
                Error::Configuration(format!("decode {}: {error}", path.display()))
            })?,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                validate_slug(&slug)?;
                TrackerConfig {
                    version: CONFIG_VERSION,
                    name: normalized_name(&name)?,
                    slug,
                    apps: Vec::new(),
                    api_tokens: Vec::new(),
                }
            }
            Err(error) => {
                return Err(Error::Configuration(format!(
                    "read {}: {error}",
                    path.display()
                )));
            }
        };
        validate(&config)?;
        let repository = Self {
            path,
            public_url,
            current: RwLock::new(config),
            mutation: Mutex::new(()),
        };
        if !tokio::fs::try_exists(&repository.path)
            .await
            .map_err(|error| Error::Configuration(error.to_string()))?
        {
            let snapshot = repository.current.read().await.clone();
            repository.save(&snapshot).await?;
        } else {
            repository.secure_existing().await?;
        }
        Ok(repository)
    }

    async fn secure_existing(&self) -> Result<()> {
        let parent = self.path.parent().ok_or_else(|| {
            Error::Configuration("configuration path has no parent directory".into())
        })?;
        set_private_directory(parent).await?;
        let file = tokio::fs::File::open(&self.path).await.map_err(|error| {
            Error::Configuration(format!("open {}: {error}", self.path.display()))
        })?;
        set_private_file(&file).await
    }

    pub async fn tracker(&self) -> TrackerConfig {
        self.current.read().await.clone()
    }

    pub fn public_url(&self) -> &str {
        &self.public_url
    }

    pub async fn apps(&self) -> Result<Vec<AppView>> {
        self.current
            .read()
            .await
            .apps
            .iter()
            .map(|app| AppView::new(app, &self.public_url))
            .collect()
    }

    pub async fn app_view(&self, id: &str) -> Result<Option<AppView>> {
        self.current
            .read()
            .await
            .apps
            .iter()
            .find(|app| app.id == id)
            .map(|app| AppView::new(app, &self.public_url))
            .transpose()
    }

    pub async fn app(&self, id: &str) -> Option<AppConfig> {
        self.current
            .read()
            .await
            .apps
            .iter()
            .find(|app| app.id == id)
            .cloned()
    }

    pub async fn app_by_project_and_key(
        &self,
        project_id: &str,
        public_key: &str,
    ) -> Option<AppConfig> {
        self.current
            .read()
            .await
            .apps
            .iter()
            .find(|app| app.project_id == project_id && app.public_key == public_key)
            .cloned()
    }

    pub async fn app_by_auth_token_hash(&self, hash: &str) -> Option<AppConfig> {
        self.current
            .read()
            .await
            .apps
            .iter()
            .find(|app| token_hash_matches(hash, &app.auth_token_hash))
            .cloned()
    }

    pub async fn api_tokens(&self) -> Vec<ApiTokenView> {
        self.current
            .read()
            .await
            .api_tokens
            .iter()
            .map(ApiTokenView::from)
            .collect()
    }

    pub async fn authenticate_api_token(
        &self,
        id: &str,
        presented_hash: &str,
    ) -> Option<ApiTokenConfig> {
        self.current
            .read()
            .await
            .api_tokens
            .iter()
            .find(|token| token.id == id && token_hash_matches(presented_hash, &token.secret_hash))
            .cloned()
    }

    pub async fn create_app(&self, input: CreateApp, now: &str) -> Result<CreatedApp> {
        let _mutation = self.mutation.lock().await;
        let name = normalized_name(&input.name)?;
        validate_slug(&input.slug)?;
        let mut replacement = self.current.read().await.clone();
        if replacement.apps.iter().any(|app| app.slug == input.slug) {
            return Err(Error::InvalidRequest(
                "application slug is already in use".into(),
            ));
        }
        let project_id = next_project_id(&replacement.apps)?;
        let public_key = Uuid::new_v4().simple().to_string();
        let auth_token = new_app_auth_token();
        let app = AppConfig {
            id: cuid2::create_id(),
            project_id: project_id.clone(),
            name,
            slug: input.slug,
            public_key,
            auth_token_hash: token_hash(&auth_token),
            webhooks: Vec::new(),
            created_at: now.to_owned(),
            updated_at: now.to_owned(),
        };
        let app_view = AppView::new(&app, &self.public_url)?;
        replacement.apps.push(app.clone());
        self.save(&replacement).await?;
        *self.current.write().await = replacement;
        Ok(CreatedApp {
            app: app_view,
            auth_token,
        })
    }

    pub async fn rotate_app_auth_token(&self, app_id: &str, now: &str) -> Result<String> {
        let _mutation = self.mutation.lock().await;
        let auth_token = new_app_auth_token();
        let mut replacement = self.current.read().await.clone();
        let app = replacement
            .apps
            .iter_mut()
            .find(|app| app.id == app_id)
            .ok_or(Error::NotFound)?;
        app.auth_token_hash = token_hash(&auth_token);
        app.updated_at = now.to_owned();
        self.save(&replacement).await?;
        *self.current.write().await = replacement;
        Ok(auth_token)
    }

    pub async fn create_api_token(
        &self,
        input: CreateApiToken,
        now: &str,
    ) -> Result<CreatedApiToken> {
        let _mutation = self.mutation.lock().await;
        let name = normalized_name(&input.name)?;
        let mut replacement = self.current.read().await.clone();
        if let Some(app_id) = input.app_id.as_deref()
            && !replacement.apps.iter().any(|app| app.id == app_id)
        {
            return Err(Error::InvalidRequest(
                "API token application does not exist".into(),
            ));
        }
        let id = Uuid::new_v4().simple().to_string();
        let secret = format!("{}{}", Uuid::new_v4().simple(), Uuid::new_v4().simple());
        let value = format!("etapi_{id}_{secret}");
        let token = ApiTokenConfig {
            id,
            name,
            role: input.role,
            app_id: input.app_id,
            secret_hash: token_hash(&value),
            created_at: now.to_owned(),
        };
        replacement.api_tokens.push(token.clone());
        self.save(&replacement).await?;
        *self.current.write().await = replacement;
        Ok(CreatedApiToken {
            api_token: ApiTokenView::from(&token),
            token: value,
        })
    }

    pub async fn revoke_api_token(&self, id: &str) -> Result<()> {
        let _mutation = self.mutation.lock().await;
        let mut replacement = self.current.read().await.clone();
        let initial = replacement.api_tokens.len();
        replacement.api_tokens.retain(|token| token.id != id);
        if replacement.api_tokens.len() == initial {
            return Err(Error::NotFound);
        }
        self.save(&replacement).await?;
        *self.current.write().await = replacement;
        Ok(())
    }

    pub async fn create_webhook(
        &self,
        app_id: &str,
        input: CreateWebhook,
        now: &str,
    ) -> Result<CreatedWebhook> {
        let _mutation = self.mutation.lock().await;
        let url = validate_webhook_url(&input.url)?;
        let events = normalized_webhook_events(input.events)?;
        let mut replacement = self.current.read().await.clone();
        let app = replacement
            .apps
            .iter_mut()
            .find(|app| app.id == app_id)
            .ok_or(Error::NotFound)?;
        let secret = format!(
            "whsec_{}{}",
            Uuid::new_v4().simple(),
            Uuid::new_v4().simple()
        );
        let webhook = WebhookConfig {
            id: Uuid::new_v4().simple().to_string(),
            url,
            secret: secret.clone(),
            events,
            enabled: true,
            created_at: now.to_owned(),
            updated_at: now.to_owned(),
        };
        app.webhooks.push(webhook.clone());
        app.updated_at = now.to_owned();
        self.save(&replacement).await?;
        *self.current.write().await = replacement;
        Ok(CreatedWebhook {
            webhook: WebhookView::from(&webhook),
            secret,
        })
    }

    pub async fn update_webhook(
        &self,
        app_id: &str,
        webhook_id: &str,
        input: UpdateWebhook,
        now: &str,
    ) -> Result<WebhookView> {
        let _mutation = self.mutation.lock().await;
        let url = validate_webhook_url(&input.url)?;
        let events = normalized_webhook_events(input.events)?;
        let mut replacement = self.current.read().await.clone();
        let app = replacement
            .apps
            .iter_mut()
            .find(|app| app.id == app_id)
            .ok_or(Error::NotFound)?;
        let webhook = app
            .webhooks
            .iter_mut()
            .find(|webhook| webhook.id == webhook_id)
            .ok_or(Error::NotFound)?;
        webhook.url = url;
        webhook.events = events;
        webhook.enabled = input.enabled;
        webhook.updated_at = now.to_owned();
        let view = WebhookView::from(&*webhook);
        app.updated_at = now.to_owned();
        self.save(&replacement).await?;
        *self.current.write().await = replacement;
        Ok(view)
    }

    pub async fn delete_webhook(&self, app_id: &str, webhook_id: &str, now: &str) -> Result<()> {
        let _mutation = self.mutation.lock().await;
        let mut replacement = self.current.read().await.clone();
        let app = replacement
            .apps
            .iter_mut()
            .find(|app| app.id == app_id)
            .ok_or(Error::NotFound)?;
        let initial = app.webhooks.len();
        app.webhooks.retain(|webhook| webhook.id != webhook_id);
        if app.webhooks.len() == initial {
            return Err(Error::NotFound);
        }
        app.updated_at = now.to_owned();
        self.save(&replacement).await?;
        *self.current.write().await = replacement;
        Ok(())
    }

    async fn save(&self, config: &TrackerConfig) -> Result<()> {
        let parent = self.path.parent().ok_or_else(|| {
            Error::Configuration("configuration path has no parent directory".into())
        })?;
        tokio::fs::create_dir_all(parent)
            .await
            .map_err(|error| Error::Configuration(format!("create volume: {error}")))?;
        set_private_directory(parent).await?;
        let bytes = serde_json::to_vec_pretty(config)
            .map_err(|error| Error::Configuration(format!("encode configuration: {error}")))?;
        let temporary = parent.join(format!(".config-{}.tmp", Uuid::new_v4().simple()));
        let result = async {
            let mut options = tokio::fs::OpenOptions::new();
            options.create_new(true).write(true);
            let mut file = options.open(&temporary).await.map_err(|error| {
                Error::Configuration(format!("create {}: {error}", temporary.display()))
            })?;
            set_private_file(&file).await?;
            file.write_all(&bytes)
                .await
                .map_err(|error| Error::Configuration(format!("write configuration: {error}")))?;
            file.write_all(b"\n")
                .await
                .map_err(|error| Error::Configuration(format!("write configuration: {error}")))?;
            file.sync_all()
                .await
                .map_err(|error| Error::Configuration(format!("sync configuration: {error}")))?;
            drop(file);
            tokio::fs::rename(&temporary, &self.path)
                .await
                .map_err(|error| Error::Configuration(format!("publish configuration: {error}")))?;
            sync_directory(parent).await
        }
        .await;
        if result.is_err() {
            let _ = tokio::fs::remove_file(&temporary).await;
        }
        result
    }
}

impl AppView {
    fn new(app: &AppConfig, public_url: &str) -> Result<Self> {
        Ok(Self {
            id: app.id.clone(),
            project_id: app.project_id.clone(),
            name: app.name.clone(),
            slug: app.slug.clone(),
            public_key: app.public_key.clone(),
            dsn: build_dsn(public_url, &app.public_key, &app.project_id)?,
            webhooks: app.webhooks.iter().map(WebhookView::from).collect(),
            created_at: app.created_at.clone(),
            updated_at: app.updated_at.clone(),
        })
    }
}

impl From<&WebhookConfig> for WebhookView {
    fn from(webhook: &WebhookConfig) -> Self {
        Self {
            id: webhook.id.clone(),
            url: webhook.url.clone(),
            events: webhook.events.clone(),
            enabled: webhook.enabled,
            created_at: webhook.created_at.clone(),
            updated_at: webhook.updated_at.clone(),
        }
    }
}

impl From<&ApiTokenConfig> for ApiTokenView {
    fn from(token: &ApiTokenConfig) -> Self {
        Self {
            id: token.id.clone(),
            name: token.name.clone(),
            role: token.role,
            app_id: token.app_id.clone(),
            created_at: token.created_at.clone(),
        }
    }
}

fn validate(config: &TrackerConfig) -> Result<()> {
    if config.version != CONFIG_VERSION {
        return Err(Error::Configuration(
            "unsupported or incomplete tracker configuration".into(),
        ));
    }
    normalized_name(&config.name)?;
    validate_slug(&config.slug)?;
    for (index, token) in config.api_tokens.iter().enumerate() {
        normalized_name(&token.name)?;
        if !valid_hex(&token.id, 32)
            || !valid_hex(&token.secret_hash, 64)
            || token
                .app_id
                .as_deref()
                .is_some_and(|id| !config.apps.iter().any(|app| app.id == id))
            || config.api_tokens[..index]
                .iter()
                .any(|candidate| candidate.id == token.id)
        {
            return Err(Error::Configuration(format!(
                "API token at index {index} is invalid"
            )));
        }
    }
    for (index, app) in config.apps.iter().enumerate() {
        normalized_name(&app.name)?;
        validate_slug(&app.slug)?;
        if !valid_cuid2(&app.id)
            || !valid_project_id(&app.project_id)
            || !valid_hex(&app.public_key, 32)
            || !valid_hex(&app.auth_token_hash, 64)
        {
            return Err(Error::Configuration(format!(
                "application at index {index} is invalid"
            )));
        }
        if config.apps[..index].iter().any(|candidate| {
            candidate.id == app.id
                || candidate.slug == app.slug
                || candidate.project_id == app.project_id
                || candidate.public_key == app.public_key
        }) {
            return Err(Error::Configuration(
                "application identifiers must be unique".into(),
            ));
        }
        for (webhook_index, webhook) in app.webhooks.iter().enumerate() {
            validate_webhook_url(&webhook.url).map_err(|error| {
                Error::Configuration(format!(
                    "application at index {index} has invalid webhook: {error}"
                ))
            })?;
            normalized_webhook_events(webhook.events.clone()).map_err(|error| {
                Error::Configuration(format!(
                    "application at index {index} has invalid webhook: {error}"
                ))
            })?;
            if !valid_hex(&webhook.id, 32)
                || webhook.secret.len() < 32
                || app.webhooks[..webhook_index]
                    .iter()
                    .any(|candidate| candidate.id == webhook.id)
            {
                return Err(Error::Configuration(format!(
                    "application at index {index} has invalid webhook identifiers"
                )));
            }
        }
    }
    Ok(())
}

fn valid_hex(value: &str, length: usize) -> bool {
    value.len() == length && value.bytes().all(|byte| byte.is_ascii_hexdigit())
}

fn valid_cuid2(value: &str) -> bool {
    value.len() == usize::from(cuid2::DEFAULT_LENGTH) && cuid2::is_cuid2(value)
}

fn valid_project_id(value: &str) -> bool {
    value
        .parse::<NonZeroU64>()
        .is_ok_and(|id| id.get().to_string() == value)
}

fn next_project_id(apps: &[AppConfig]) -> Result<String> {
    let maximum = apps.iter().try_fold(0_u64, |maximum, app| {
        let current = app.project_id.parse::<u64>().map_err(|_| {
            Error::Configuration("application has an invalid Sentry project ID".into())
        })?;
        Ok::<_, Error>(maximum.max(current))
    })?;
    maximum
        .checked_add(1)
        .map(|project_id| project_id.to_string())
        .ok_or_else(|| Error::Configuration("Sentry project ID space is exhausted".into()))
}

fn normalized_name(value: &str) -> Result<String> {
    let value = value.trim();
    if value.is_empty() || value.chars().count() > 80 || value.chars().any(char::is_control) {
        return Err(Error::InvalidRequest(
            "name must contain 1 to 80 printable characters".into(),
        ));
    }
    Ok(value.to_owned())
}

fn validate_slug(value: &str) -> Result<()> {
    if value.is_empty()
        || value.len() > 48
        || value.starts_with('-')
        || value.ends_with('-')
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
    {
        return Err(Error::InvalidRequest(
            "slug must contain lowercase letters, digits, or interior hyphens".into(),
        ));
    }
    Ok(())
}

fn normalize_public_url(value: &str) -> Result<Url> {
    let url =
        Url::parse(value).map_err(|_| Error::InvalidRequest("public URL is invalid".into()))?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(Error::InvalidRequest(
            "public URL must be an HTTP(S) origin without a path, credentials, query, or fragment"
                .into(),
        ));
    }
    Ok(url)
}

fn normalized_public_url(value: &str) -> Result<String> {
    let url = normalize_public_url(value)?;
    Ok(url
        .as_str()
        .strip_suffix('/')
        .unwrap_or(url.as_str())
        .to_owned())
}

fn build_dsn(public_url: &str, public_key: &str, project_id: &str) -> Result<String> {
    let mut url = normalize_public_url(public_url)?;
    url.set_username(public_key)
        .map_err(|()| Error::Configuration("public key cannot be encoded in DSN".into()))?;
    let path = format!("{}{project_id}", url.path());
    url.set_path(&path);
    Ok(url.to_string())
}

fn validate_webhook_url(value: &str) -> Result<String> {
    let url =
        Url::parse(value).map_err(|_| Error::InvalidRequest("webhook URL is invalid".into()))?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.fragment().is_some()
        || value.len() > 2048
    {
        return Err(Error::InvalidRequest(
            "webhook URL must be an HTTP(S) URL without credentials or fragment".into(),
        ));
    }
    Ok(url.to_string())
}

fn normalized_webhook_events(mut events: Vec<WebhookEvent>) -> Result<Vec<WebhookEvent>> {
    if events.is_empty() {
        return Err(Error::InvalidRequest(
            "select at least one webhook event".into(),
        ));
    }
    events.sort_by_key(|event| match event {
        WebhookEvent::IssueCreated => 0,
        WebhookEvent::IssueRegressed => 1,
        WebhookEvent::IssueResolved => 2,
        WebhookEvent::EventReceived => 3,
    });
    events.dedup();
    Ok(events)
}

#[cfg(unix)]
async fn set_private_directory(path: &Path) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;
    tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700))
        .await
        .map_err(|error| Error::Configuration(format!("secure volume directory: {error}")))
}

#[cfg(not(unix))]
async fn set_private_directory(_path: &Path) -> Result<()> {
    Ok(())
}

#[cfg(unix)]
async fn set_private_file(file: &tokio::fs::File) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;
    file.set_permissions(std::fs::Permissions::from_mode(0o600))
        .await
        .map_err(|error| Error::Configuration(format!("secure configuration file: {error}")))
}

#[cfg(not(unix))]
async fn set_private_file(_file: &tokio::fs::File) -> Result<()> {
    Ok(())
}

#[cfg(unix)]
async fn sync_directory(path: &Path) -> Result<()> {
    let directory = tokio::fs::File::open(path)
        .await
        .map_err(|error| Error::Configuration(format!("open volume directory: {error}")))?;
    directory
        .sync_all()
        .await
        .map_err(|error| Error::Configuration(format!("sync volume directory: {error}")))
}

#[cfg(not(unix))]
async fn sync_directory(_path: &Path) -> Result<()> {
    Ok(())
}

fn new_app_auth_token() -> String {
    format!("et_{}{}", Uuid::new_v4().simple(), Uuid::new_v4().simple())
}

#[cfg(test)]
mod tests {
    use tempfile::tempdir;

    use super::*;

    #[test]
    fn public_url_must_be_an_origin() {
        assert!(normalize_public_url("https://errors.example.com").is_ok());
        assert!(normalize_public_url("https://errors.example.com/base").is_err());
        assert!(normalize_public_url("https://user@errors.example.com").is_err());
        assert!(normalize_public_url("https://errors.example.com?tenant=one").is_err());
    }

    #[test]
    fn name_limit_counts_characters_not_utf8_bytes() {
        assert!(normalized_name(&"я".repeat(80)).is_ok());
        assert!(normalized_name(&"я".repeat(81)).is_err());
    }

    #[tokio::test]
    async fn persists_apps_and_only_returns_upload_token_once() {
        let directory = tempdir().unwrap();
        let path = directory.path().join("config.json");
        let repository = ConfigRepository::open(
            path.clone(),
            "Tracker".into(),
            "tracker".into(),
            "https://errors.example.com".into(),
        )
        .await
        .unwrap();
        let created = repository
            .create_app(
                CreateApp {
                    name: "Web".into(),
                    slug: "web".into(),
                },
                "2026-08-08T12:00:00Z",
            )
            .await
            .unwrap();
        assert_eq!(
            created.app.dsn,
            format!(
                "https://{}@errors.example.com/{}",
                created.app.public_key, created.app.project_id
            )
        );
        assert!(valid_cuid2(&created.app.id));
        assert_eq!(created.app.project_id, "1");
        assert!(created.auth_token.starts_with("et_"));

        let second = repository
            .create_app(
                CreateApp {
                    name: "Worker".into(),
                    slug: "worker".into(),
                },
                "2026-08-08T12:00:30Z",
            )
            .await
            .unwrap();
        assert_eq!(second.app.project_id, "2");

        let persisted: serde_json::Value =
            serde_json::from_slice(&tokio::fs::read(&path).await.unwrap()).unwrap();
        assert!(persisted.get("trackerId").is_none());
        assert!(persisted.get("publicUrl").is_none());
        assert!(persisted.pointer("/apps/0/dsn").is_none());

        let api_token = repository
            .create_api_token(
                CreateApiToken {
                    name: "Automation".into(),
                    role: ApiTokenRole::Read,
                    app_id: Some(created.app.id.clone()),
                },
                "2026-08-08T12:01:00Z",
            )
            .await
            .unwrap();
        let token_id = crate::auth::api_token_id(&api_token.token).unwrap();
        assert!(
            repository
                .authenticate_api_token(token_id, &token_hash(&api_token.token))
                .await
                .is_some()
        );

        let reopened = ConfigRepository::open(
            path,
            "ignored".into(),
            "ignored".into(),
            "https://ignored.example.com".into(),
        )
        .await
        .unwrap();
        assert_eq!(
            reopened.apps().await.unwrap()[0].dsn,
            format!(
                "https://{}@ignored.example.com/{}",
                created.app.public_key, created.app.project_id
            )
        );
        assert_eq!(reopened.api_tokens().await.len(), 1);
    }

    #[tokio::test]
    async fn rotating_upload_token_invalidates_the_previous_token() {
        let directory = tempdir().unwrap();
        let path = directory.path().join("config.json");
        let repository = ConfigRepository::open(
            path.clone(),
            "Tracker".into(),
            "tracker".into(),
            "https://errors.example.com".into(),
        )
        .await
        .unwrap();
        let created = repository
            .create_app(
                CreateApp {
                    name: "Web".into(),
                    slug: "web".into(),
                },
                "2026-08-08T12:00:00Z",
            )
            .await
            .unwrap();

        let rotated = repository
            .rotate_app_auth_token(&created.app.id, "2026-08-08T12:01:00Z")
            .await
            .unwrap();

        assert_ne!(rotated, created.auth_token);
        assert!(
            repository
                .app_by_auth_token_hash(&token_hash(&created.auth_token))
                .await
                .is_none()
        );
        assert!(
            repository
                .app_by_auth_token_hash(&token_hash(&rotated))
                .await
                .is_some()
        );

        let reopened = ConfigRepository::open(
            path,
            "ignored".into(),
            "ignored".into(),
            "https://ignored.example.com".into(),
        )
        .await
        .unwrap();
        assert!(
            reopened
                .app_by_auth_token_hash(&token_hash(&created.auth_token))
                .await
                .is_none()
        );
        assert!(
            reopened
                .app_by_auth_token_hash(&token_hash(&rotated))
                .await
                .is_some()
        );
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn reapplies_private_permissions_when_reopening_configuration() {
        use std::os::unix::fs::PermissionsExt;

        let directory = tempdir().unwrap();
        let path = directory.path().join("config.json");
        ConfigRepository::open(
            path.clone(),
            "Tracker".into(),
            "tracker".into(),
            "https://errors.example.com".into(),
        )
        .await
        .unwrap();

        std::fs::set_permissions(directory.path(), std::fs::Permissions::from_mode(0o755)).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o644)).unwrap();

        ConfigRepository::open(
            path.clone(),
            "ignored".into(),
            "ignored".into(),
            "https://ignored.example.com".into(),
        )
        .await
        .unwrap();

        assert_eq!(
            std::fs::metadata(directory.path())
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o700
        );
        assert_eq!(
            std::fs::metadata(path).unwrap().permissions().mode() & 0o777,
            0o600
        );
    }
}
