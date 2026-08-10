use axum::Json;
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use serde::Serialize;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum Error {
    #[error("authentication failed")]
    Authentication,
    #[error("access is forbidden")]
    Forbidden,
    #[error("configuration error: {0}")]
    Configuration(String),
    #[error("invalid request: {0}")]
    InvalidRequest(String),
    #[error("resource not found")]
    NotFound,
    #[error("storage error: {0}")]
    Storage(String),
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct ErrorBody {
    code: &'static str,
    message: String,
}

impl IntoResponse for Error {
    fn into_response(self) -> Response {
        let (status, code) = match &self {
            Self::Authentication => (StatusCode::UNAUTHORIZED, "authentication_failed"),
            Self::Forbidden => (StatusCode::FORBIDDEN, "forbidden"),
            Self::Configuration(_) => (StatusCode::INTERNAL_SERVER_ERROR, "configuration_error"),
            Self::InvalidRequest(_) => (StatusCode::BAD_REQUEST, "invalid_request"),
            Self::NotFound => (StatusCode::NOT_FOUND, "not_found"),
            Self::Storage(_) => (StatusCode::INTERNAL_SERVER_ERROR, "storage_error"),
        };
        let message = self.to_string();
        (status, Json(ErrorBody { code, message })).into_response()
    }
}

pub type Result<T> = std::result::Result<T, Error>;
