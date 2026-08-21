#!/bin/sh
set -eu

command -v curl >/dev/null
command -v docker >/dev/null
command -v jq >/dev/null
command -v gzip >/dev/null

configuration_url=${PLATFORMD_TELEMETRY_TEST_CONFIGURATION_URL:-}
if [ -n "$configuration_url" ]; then
  configuration=$(curl -fsS "$configuration_url")
  dsn=$(printf '%s' "$configuration" | jq -er .dsn)
  host_dsn=$(printf '%s' "$configuration" | jq -er .hostDsn)
  service_id=$(printf '%s' "$configuration" | jq -er .serviceId)
else
  dsn=${PLATFORMD_TELEMETRY_TEST_DSN:?set PLATFORMD_TELEMETRY_TEST_DSN or PLATFORMD_TELEMETRY_TEST_CONFIGURATION_URL}
  host_dsn=${PLATFORMD_TELEMETRY_TEST_HOST_DSN:-$dsn}
  service_id=${PLATFORMD_TELEMETRY_TEST_SERVICE_ID:?set PLATFORMD_TELEMETRY_TEST_SERVICE_ID}
fi
api_url=${PLATFORMD_TELEMETRY_TEST_API_URL:?set PLATFORMD_TELEMETRY_TEST_API_URL to the common API service errors endpoint}
api_token=${PLATFORMD_TELEMETRY_TEST_API_TOKEN:?set PLATFORMD_TELEMETRY_TEST_API_TOKEN to a read API token}
artifact_token=${PLATFORMD_TELEMETRY_TEST_ARTIFACT_TOKEN:?set PLATFORMD_TELEMETRY_TEST_ARTIFACT_TOKEN to the service artifact token}
selected_case=${CONFORMANCE_CASE:-}
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
run_id=$(date +%s)-$$
active_image=
active_tag=

api_get() {
  curl -fsS "${api_url%/}/$1" -H "Authorization: Bearer $api_token"
}

cleanup_image() {
  if [ -z "$active_tag" ]; then
    return
  fi
  docker image rm --force "$active_tag" >/dev/null 2>&1 || true
  docker builder prune --all --force >/dev/null
  active_image=
  active_tag=
}

prepare_image() {
  image=$1
  if [ "$active_image" = "$image" ]; then
    return
  fi
  # cases.tsv groups cases by image. Reuse that image within the group, then
  # prune before the next language so the hosted runner does not run out of disk.
  cleanup_image
  active_tag="platformd/telemetry-conformance-$image:local"
  docker build --quiet --tag "$active_tag" "$root/clients/$image" >/dev/null
  active_image=$image
}

trap cleanup_image EXIT

find_case_event() {
  case_name=$1
  event_ids=$(api_get "events?limit=100&query=$case_name" | jq -r '.data[].event_id')
  for event_id in $event_ids; do
    detail=$(api_get "events/$event_id")
    if printf '%s' "$detail" | jq -e --arg case_name "$case_name" \
      '.event.payload.tags.conformance_case == $case_name and (.event.sdk_name | length > 0)' >/dev/null; then
      printf '%s\n' "$event_id"
      return
    fi
  done
}

has_failed_crash_event() {
  event_ids=$(api_get "events?limit=100&query=symbolicator" | jq -r '.data[].event_id')
  for event_id in $event_ids; do
    detail=$(api_get "events/$event_id")
    if printf '%s' "$detail" | jq -e \
      '.event.payload.contexts.symbolicator.status == "failed"' >/dev/null; then
      return 0
    fi
  done
  return 1
}

run_case() {
  image=$1
  case_name=$2
  if [ -n "$selected_case" ] && [ "$selected_case" != "$case_name" ]; then
    return
  fi

  prepare_image "$image"
  dsn_origin=$(printf '%s' "$dsn" | sed -E 's#(https?://)[^@]+@#\1#; s#/1/?$##')
  docker run --rm \
    --add-host host.docker.internal:host-gateway \
    -e "SENTRY_DSN=$dsn" \
    -e "SENTRY_URL=$dsn_origin" \
    -e "SENTRY_AUTH_TOKEN=$artifact_token" \
    -e "SENTRY_ORG=platformd" \
    -e "SENTRY_PROJECT=$service_id" \
    -e "CONFORMANCE_CASE=$case_name" \
    "$active_tag"

  attempt=0
  while [ "$attempt" -lt 20 ]; do
    event_id=$(find_case_event "$case_name" || true)
    if [ -n "$event_id" ] && [ "$case_name" != "sourcemap" ]; then
      printf 'PASS %s\n' "$case_name"
      return
    fi
    if [ -n "$event_id" ] && [ "$case_name" = "sourcemap" ]; then
      detail=$(api_get "events/$event_id")
      if printf '%s' "$detail" | jq -e \
        '.symbolication.status == "completed" and .symbolication.payload.stacktraces[0].frames[0].filename == "app.js"' >/dev/null; then
        printf 'PASS %s\n' "$case_name"
        return
      fi
    fi
    attempt=$((attempt + 1))
    sleep 1
  done
  printf 'FAIL %s: SDK event was not stored\n' "$case_name" >&2
  return 1
}

run_protocol_checks() {
  public_key=$(printf '%s' "$host_dsn" | sed -E 's#https?://([^@]+)@.*#\1#')
  project_id=$(printf '%s' "$host_dsn" | sed -E 's#^.*/([^/]+)/?$#\1#')
  origin=$(printf '%s' "$host_dsn" | sed -E 's#(https?://)[^@]+@#\1#; s#/[^/]+/?$##')
  [ "$public_key" = "$service_id" ]
  [ "$project_id" = 1 ]
  ingest_url="$origin/api/1/envelope/"
  sentry_auth="Sentry sentry_version=7, sentry_key=$public_key"

  empty_status=$(printf '{}\n' | curl -sS -o /dev/null -w '%{http_code}' \
    -X POST "$ingest_url" -H "X-Sentry-Auth: $sentry_auth" --data-binary @-)
  [ "$empty_status" = 400 ]

  malformed_status=$(printf '{}\n{"type":"event","length":8}\nnot-json' | \
    curl -sS -o /dev/null -w '%{http_code}' -X POST "$ingest_url" \
      -H "X-Sentry-Auth: $sentry_auth" --data-binary @-)
  [ "$malformed_status" = 400 ]

  duplicate_id=dddddddddddddddddddddddddddddddd
  duplicate_payload="{\"event_id\":\"$duplicate_id\",\"platform\":\"javascript\",\"message\":\"protocol duplicate\",\"tags\":{\"conformance_case\":\"protocol\"}}"
  duplicate_length=$(printf '%s' "$duplicate_payload" | wc -c | tr -d ' ')
  duplicate_envelope=$(printf '{"event_id":"%s"}\n{"type":"event","length":%s}\n%s' \
    "$duplicate_id" "$duplicate_length" "$duplicate_payload")
  printf '%s' "$duplicate_envelope" | curl -fsS -X POST "$ingest_url" \
    -H "X-Sentry-Auth: $sentry_auth" --data-binary @- >/dev/null
  printf '%s' "$duplicate_envelope" | curl -fsS -X POST "$ingest_url" \
    -H "X-Sentry-Auth: $sentry_auth" --data-binary @- >/dev/null
  duplicate_count=$(api_get "events?limit=100&query=protocol" | jq -er --arg id "$duplicate_id" \
    '[.data[] | select(.event_id == $id)] | length')
  [ "$duplicate_count" = 1 ]

  compressed_id=cccccccccccccccccccccccccccccccc
  compressed_payload="{\"event_id\":\"$compressed_id\",\"platform\":\"python\",\"message\":\"compressed protocol event\"}"
  compressed_length=$(printf '%s' "$compressed_payload" | wc -c | tr -d ' ')
  {
    printf '{"event_id":"%s"}\n' "$compressed_id"
    printf '{"type":"event","length":%s}\n' "$compressed_length"
    printf '%s' "$compressed_payload"
  } | gzip -c | curl -fsS -X POST "$ingest_url" \
    -H "X-Sentry-Auth: $sentry_auth" \
    -H 'Content-Encoding: gzip' --data-binary @- >/dev/null

  replay_id=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  segment=0
  while [ "$segment" -lt 2 ]; do
    replay_event="{\"event_id\":\"$replay_id\",\"replay_id\":\"$replay_id\",\"segment_id\":$segment}"
    replay_recording=$(printf '{"segment_id":%s}\n[]' "$segment")
    replay_event_length=$(printf '%s' "$replay_event" | wc -c | tr -d ' ')
    replay_recording_length=$(printf '%s' "$replay_recording" | wc -c | tr -d ' ')
    {
      printf '{"event_id":"%s"}\n' "$replay_id"
      printf '{"type":"replay_event","length":%s}\n%s\n' "$replay_event_length" "$replay_event"
      printf '{"type":"replay_recording","length":%s}\n%s' "$replay_recording_length" "$replay_recording"
    } | curl -fsS -X POST "$ingest_url" \
      -H "X-Sentry-Auth: $sentry_auth" --data-binary @- >/dev/null
    segment=$((segment + 1))
  done
  replay_total=$(api_get "replays/$replay_id" | jq -er .total)
  [ "$replay_total" = 4 ]

  boundary="platformd-telemetry-conformance-$run_id"
  {
    printf -- '--%s\r\n' "$boundary"
    printf 'Content-Disposition: form-data; name="upload_file_minidump"; filename="invalid.dmp"\r\n'
    printf 'Content-Type: application/octet-stream\r\n\r\n'
    printf 'not a minidump\r\n'
    printf -- '--%s--\r\n' "$boundary"
  } | curl -fsS -X POST "$origin/api/1/minidump/" \
    -H "X-Sentry-Auth: $sentry_auth" \
    -H "Content-Type: multipart/form-data; boundary=$boundary" \
    --data-binary @- >/dev/null
  has_failed_crash_event

  printf 'PASS protocol\n'
}

while IFS="$(printf '\t')" read -r image case_name; do
  case "$image" in ''|'#'*) continue ;; esac
  run_case "$image" "$case_name"
done < "$root/cases.tsv"

if [ -z "$selected_case" ] || [ "$selected_case" = protocol ]; then
  run_protocol_checks
fi
