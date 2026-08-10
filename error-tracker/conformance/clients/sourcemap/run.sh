#!/bin/sh
set -eu

release=conformance-sourcemap@1.0
sentry-cli sourcemaps upload --release "$release" --no-rewrite /case/dist

project_id=${SENTRY_DSN##*/}
public_key=${SENTRY_DSN#*://}
public_key=${public_key%%@*}
event_id=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
payload="{\"event_id\":\"$event_id\",\"platform\":\"javascript\",\"release\":\"$release\",\"level\":\"error\",\"tags\":{\"conformance_case\":\"sourcemap\"},\"exception\":{\"values\":[{\"type\":\"Error\",\"value\":\"source map conformance\",\"stacktrace\":{\"frames\":[{\"filename\":\"app.min.js\",\"abs_path\":\"http://example.invalid/app.min.js\",\"function\":\"boom\",\"lineno\":1,\"colno\":23,\"in_app\":true}]}}]},\"sdk\":{\"name\":\"sentry.javascript.browser\",\"version\":\"10.69.0\"}}"
length=$(printf '%s' "$payload" | wc -c | tr -d ' ')
{
  printf '{"event_id":"%s"}\n' "$event_id"
  printf '{"type":"event","length":%s,"content_type":"application/json"}\n' "$length"
  printf '%s\n' "$payload"
} | curl -fsS -X POST "${SENTRY_URL%/}/api/$project_id/envelope/" \
  -H "X-Sentry-Auth: Sentry sentry_version=7, sentry_key=$public_key" \
  -H 'Content-Type: application/x-sentry-envelope' \
  --data-binary @- >/dev/null
