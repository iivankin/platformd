require "sentry-ruby"

test_case = ENV.fetch("CONFORMANCE_CASE")
Sentry.init do |config|
  config.dsn = ENV.fetch("SENTRY_DSN")
  config.background_worker_threads = 1
end
Sentry.set_tags(conformance_case: test_case)
Sentry.capture_exception(RuntimeError.new("ruby conformance"))
Sentry.close
