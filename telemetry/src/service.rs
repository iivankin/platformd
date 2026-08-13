use crate::error::{Error, Result};

pub const SENTRY_PROJECT_ID: &str = "1";
pub const SENTRY_ORGANIZATION: &str = "platformd";
pub const SERVICE_ID_HEADER: &str = "x-platformd-service-id";
pub const ARTIFACT_AUTHORIZED_HEADER: &str = "x-platformd-artifact-authorized";
pub const WEBHOOK_EVENTS_HEADER: &str = "x-platformd-telemetry-events";

#[derive(Clone, Debug)]
pub struct ServiceContext {
    pub id: String,
}

impl ServiceContext {
    pub fn parse(value: &str) -> Result<Self> {
        if value.is_empty()
            || value.len() > 128
            || !value
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-'))
        {
            return Err(Error::Authentication);
        }
        Ok(Self {
            id: value.to_owned(),
        })
    }
}
