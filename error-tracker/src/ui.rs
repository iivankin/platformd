use axum::http::{StatusCode, header};
use axum::response::IntoResponse;

pub async fn index() -> impl IntoResponse {
    asset(
        "text/html; charset=utf-8",
        include_str!("web/dist/index.html"),
    )
}

pub async fn styles() -> impl IntoResponse {
    asset(
        "text/css; charset=utf-8",
        include_str!("web/dist/index.css"),
    )
}

pub async fn application() -> impl IntoResponse {
    asset(
        "text/javascript; charset=utf-8",
        include_str!("web/dist/index.js"),
    )
}

fn asset(content_type: &'static str, body: &'static str) -> impl IntoResponse {
    (
        StatusCode::OK,
        [
            (header::CONTENT_TYPE, content_type),
            (header::CACHE_CONTROL, "no-cache"),
            (header::X_CONTENT_TYPE_OPTIONS, "nosniff"),
        ],
        body,
    )
}
