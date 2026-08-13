use axum::http::HeaderMap;
use url::form_urlencoded;

pub fn sentry_public_key(headers: &HeaderMap, query: Option<&str>) -> Option<String> {
    if let Some(value) = headers
        .get("x-sentry-auth")
        .and_then(|value| value.to_str().ok())
        && let Some(key) = sentry_auth_value(value, "sentry_key")
    {
        return Some(key.to_owned());
    }
    query.and_then(|query| {
        form_urlencoded::parse(query.as_bytes())
            .find_map(|(key, value)| (key == "sentry_key").then(|| value.into_owned()))
    })
}

fn sentry_auth_value<'a>(header: &'a str, key: &str) -> Option<&'a str> {
    let (_, values) = header.split_once(' ')?;
    values.split(',').find_map(|entry| {
        let (name, value) = entry.trim().split_once('=')?;
        (name == key).then_some(value.trim())
    })
}

#[cfg(test)]
mod tests {
    use axum::http::{HeaderMap, HeaderValue};

    use super::*;

    #[test]
    fn reads_header_before_query_authentication() {
        let mut headers = HeaderMap::new();
        headers.insert(
            "x-sentry-auth",
            HeaderValue::from_static("Sentry sentry_version=7, sentry_key=header-key"),
        );
        assert_eq!(
            sentry_public_key(&headers, Some("sentry_key=query-key")).as_deref(),
            Some("header-key")
        );
    }
}
