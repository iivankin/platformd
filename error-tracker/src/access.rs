use serde::Serialize;

use crate::config::{ApiTokenConfig, ApiTokenRole};
use crate::error::{Error, Result};

#[derive(Clone, Debug)]
pub struct AccessIdentity {
    token_id: Option<String>,
    role: ApiTokenRole,
    app_id: Option<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AccessIdentityView {
    token_id: Option<String>,
    role: ApiTokenRole,
    app_id: Option<String>,
}

impl AccessIdentity {
    pub fn management() -> Self {
        Self {
            token_id: None,
            role: ApiTokenRole::Admin,
            app_id: None,
        }
    }

    pub fn api_token(token: &ApiTokenConfig) -> Self {
        Self {
            token_id: Some(token.id.clone()),
            role: token.role,
            app_id: token.app_id.clone(),
        }
    }

    pub fn is_admin(&self) -> bool {
        self.role == ApiTokenRole::Admin
    }

    pub fn app_id(&self) -> Option<&str> {
        self.app_id.as_deref()
    }

    pub fn require_app_read(&self, app_id: &str) -> Result<()> {
        if self.app_id.as_deref().is_none_or(|scope| scope == app_id) {
            Ok(())
        } else {
            Err(Error::Forbidden)
        }
    }

    pub fn require_app_admin(&self, app_id: &str) -> Result<()> {
        if !self.is_admin() {
            return Err(Error::Forbidden);
        }
        self.require_app_read(app_id)
    }

    pub fn require_unscoped_admin(&self) -> Result<()> {
        if self.is_admin() && self.app_id.is_none() {
            Ok(())
        } else {
            Err(Error::Forbidden)
        }
    }

    pub fn view(&self) -> AccessIdentityView {
        AccessIdentityView {
            token_id: self.token_id.clone(),
            role: self.role,
            app_id: self.app_id.clone(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn enforces_role_and_application_scope() {
        let read = AccessIdentity {
            token_id: Some("read".into()),
            role: ApiTokenRole::Read,
            app_id: Some("app-a".into()),
        };
        assert!(read.require_app_read("app-a").is_ok());
        assert!(read.require_app_read("app-b").is_err());
        assert!(read.require_app_admin("app-a").is_err());

        let admin = AccessIdentity {
            token_id: Some("admin".into()),
            role: ApiTokenRole::Admin,
            app_id: None,
        };
        assert!(admin.require_app_admin("app-b").is_ok());
        assert!(admin.require_unscoped_admin().is_ok());
    }
}
