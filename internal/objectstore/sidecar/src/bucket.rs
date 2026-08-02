pub fn physical_bucket_name(store_id: &str) -> Result<String, String> {
    if !is_cuid2(store_id) {
        return Err("invalid object store identity".to_owned());
    }
    Ok(format!("pd-{store_id}"))
}

pub fn is_managed_bucket(name: &str) -> bool {
    let Some(store_id) = name.strip_prefix("pd-") else {
        return false;
    };
    is_cuid2(store_id)
}

fn is_cuid2(value: &str) -> bool {
    value.len() == 24
        && value
            .bytes()
            .next()
            .is_some_and(|byte| byte.is_ascii_lowercase())
        && value
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit())
}

#[cfg(test)]
mod tests {
    use super::{is_managed_bucket, physical_bucket_name};

    #[test]
    fn physical_names_are_stable_and_recognizable() {
        let name = physical_bucket_name("v1w2x3y4z5a6b7c8d9e0f1g2").expect("name");
        assert_eq!(name, "pd-v1w2x3y4z5a6b7c8d9e0f1g2");
        assert!(is_managed_bucket(&name));
        assert!(physical_bucket_name("019fabcd-1234-7abc-8def-0123456789ab").is_err());
        assert!(!is_managed_bucket("rustfs-system"));
    }
}
