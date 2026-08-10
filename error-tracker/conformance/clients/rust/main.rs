fn main() {
    let case = std::env::var("CONFORMANCE_CASE").expect("CONFORMANCE_CASE");
    let dsn = std::env::var("SENTRY_DSN").expect("SENTRY_DSN");
    let guard = sentry::init((dsn, sentry::ClientOptions::default()));
    sentry::configure_scope(|scope| scope.set_tag("conformance_case", case));
    sentry::capture_message("rust conformance", sentry::Level::Error);
    drop(guard);
}
