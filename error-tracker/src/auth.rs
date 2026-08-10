use axum::http::HeaderMap;
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use url::form_urlencoded;

use crate::error::{Error, Result};

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

pub fn require_bearer(headers: &HeaderMap, expected_hash: &str) -> Result<()> {
    let actual = bearer_token_hash(headers).ok_or(Error::Authentication)?;
    if token_hash_matches(&actual, expected_hash) {
        Ok(())
    } else {
        Err(Error::Authentication)
    }
}

pub fn bearer_token_hash(headers: &HeaderMap) -> Option<String> {
    bearer_token(headers).map(token_hash)
}

pub fn bearer_token(headers: &HeaderMap) -> Option<&str> {
    let mut values = headers.get_all("authorization").iter();
    let value = values.next()?.to_str().ok()?;
    if values.next().is_some() {
        return None;
    }
    let (scheme, value) = value.split_once(' ')?;
    (scheme.eq_ignore_ascii_case("bearer")
        && !value.is_empty()
        && !value.chars().any(char::is_whitespace))
    .then_some(value)
}

pub fn api_token_id(token: &str) -> Option<&str> {
    let (id, secret) = token.strip_prefix("etapi_")?.split_once('_')?;
    (id.len() == 32
        && id.bytes().all(|byte| byte.is_ascii_hexdigit())
        && secret.len() == 64
        && secret.bytes().all(|byte| byte.is_ascii_hexdigit()))
    .then_some(id)
}

pub fn token_hash(token: &str) -> String {
    format!("{:x}", Sha256::digest(token.as_bytes()))
}

pub fn token_hash_matches(actual: &str, expected: &str) -> bool {
    actual.as_bytes().ct_eq(expected.as_bytes()).into()
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

    #[test]
    fn compares_bearer_tokens_by_hash() {
        let mut headers = HeaderMap::new();
        headers.insert("authorization", HeaderValue::from_static("bearer secret"));
        assert!(require_bearer(&headers, &token_hash("secret")).is_ok());
        assert!(require_bearer(&headers, &token_hash("different")).is_err());

        headers.insert(
            "authorization",
            HeaderValue::from_static("Bearer secret with-spaces"),
        );
        assert!(bearer_token(&headers).is_none());
    }

    #[test]
    fn parses_only_well_formed_api_tokens() {
        let id = "0123456789abcdef0123456789abcdef";
        let secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
        assert_eq!(api_token_id(&format!("etapi_{id}_{secret}")), Some(id));
        assert_eq!(api_token_id(&format!("etapi_{id}_short")), None);
    }
}
