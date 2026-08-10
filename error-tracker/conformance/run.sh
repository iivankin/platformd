#!/bin/sh
set -eu

tracker_url=${ERROR_TRACKER_URL:-http://127.0.0.1:8080}
container_url=${ERROR_TRACKER_CONTAINER_URL:-http://host.docker.internal:8080}
admin_token=${ERROR_TRACKER_ADMIN_TOKEN:?set ERROR_TRACKER_ADMIN_TOKEN}
selected_case=${CONFORMANCE_CASE:-}
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
run_id=$(date +%s)-$$

command -v curl >/dev/null
command -v docker >/dev/null
command -v jq >/dev/null
command -v gzip >/dev/null

curl -fsS "$tracker_url/health" >/dev/null
tracker=$(curl -fsS "$tracker_url/api/v1/tracker" -H "Authorization: Bearer $admin_token")
tracker_slug=$(printf '%s' "$tracker" | jq -er .slug)
configured_public_url=$(printf '%s' "$tracker" | jq -er .publicUrl)
if [ "${configured_public_url%/}" != "${container_url%/}" ]; then
  printf 'ERROR_TRACKER_PUBLIC_URL must be %s for the conformance containers (got %s)\n' \
    "${container_url%/}" "${configured_public_url%/}" >&2
  exit 1
fi

find_case_event() {
  app_id=$1
  case_name=$2
  event_ids=$(curl -fsS "$tracker_url/api/v1/apps/$app_id/events?limit=100" \
    -H "Authorization: Bearer $admin_token" | jq -r '.data[].event_id')
  for event_id in $event_ids; do
    detail=$(curl -fsS "$tracker_url/api/v1/apps/$app_id/events/$event_id" \
      -H "Authorization: Bearer $admin_token")
    if printf '%s' "$detail" | jq -e --arg case_name "$case_name" \
      '.event.payload.tags.conformance_case == $case_name and (.event.sdk_name | length > 0)' >/dev/null; then
      printf '%s\n' "$event_id"
      return
    fi
  done
}

has_failed_crash_event() {
  app_id=$1
  event_ids=$(curl -fsS "$tracker_url/api/v1/apps/$app_id/events?limit=100" \
    -H "Authorization: Bearer $admin_token" | jq -r '.data[].event_id')
  for event_id in $event_ids; do
    detail=$(curl -fsS "$tracker_url/api/v1/apps/$app_id/events/$event_id" \
      -H "Authorization: Bearer $admin_token")
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
  slug=$(printf 'conformance-%s-%s' "$case_name" "$run_id" | tr '_' '-')
  created=$(curl -fsS "$tracker_url/api/v1/apps" \
    -H "Authorization: Bearer $admin_token" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"Conformance $case_name\",\"slug\":\"$slug\"}")
  app_id=$(printf '%s' "$created" | jq -er .id)
  upload_token=$(printf '%s' "$created" | jq -er .authToken)
  dsn=$(printf '%s' "$created" | jq -er .dsn)

  tag="platformd/error-tracker-conformance-$image:local"
  docker build --quiet --tag "$tag" "$root/clients/$image" >/dev/null
  docker run --rm \
    --add-host host.docker.internal:host-gateway \
    -e "SENTRY_DSN=$dsn" \
    -e "SENTRY_URL=${configured_public_url%/}" \
    -e "SENTRY_AUTH_TOKEN=$upload_token" \
    -e "SENTRY_ORG=$tracker_slug" \
    -e "SENTRY_PROJECT=$slug" \
    -e "CONFORMANCE_CASE=$case_name" \
    "$tag"

  attempt=0
  while [ "$attempt" -lt 20 ]; do
    event_id=$(find_case_event "$app_id" "$case_name" || true)
    if [ -n "$event_id" ] && [ "$case_name" != "sourcemap" ]; then
      printf 'PASS %s\n' "$case_name"
      return
    fi
    if [ -n "$event_id" ] && [ "$case_name" = "sourcemap" ]; then
      detail=$(curl -fsS "$tracker_url/api/v1/apps/$app_id/events/$event_id" \
        -H "Authorization: Bearer $admin_token")
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
  slug="conformance-protocol-$run_id"
  created=$(curl -fsS "$tracker_url/api/v1/apps" \
    -H "Authorization: Bearer $admin_token" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"Conformance protocol\",\"slug\":\"$slug\"}")
  app_id=$(printf '%s' "$created" | jq -er .id)
  public_key=$(printf '%s' "$created" | jq -er .publicKey)
  project_id=$(printf '%s' "$created" | jq -er .projectId)
  ingest_url="$tracker_url/api/$project_id/envelope/"
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
  duplicate_count=$(curl -fsS "$tracker_url/api/v1/apps/$app_id/events?limit=100" \
    -H "Authorization: Bearer $admin_token" | jq -er --arg id "$duplicate_id" \
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
  replay_event="{\"event_id\":\"$replay_id\",\"replay_id\":\"$replay_id\",\"segment_id\":0}"
  replay_recording=$(printf '{"segment_id":0}\n[]')
  replay_event_length=$(printf '%s' "$replay_event" | wc -c | tr -d ' ')
  replay_recording_length=$(printf '%s' "$replay_recording" | wc -c | tr -d ' ')
  {
    printf '{"event_id":"%s"}\n' "$replay_id"
    printf '{"type":"replay_event","length":%s}\n%s\n' "$replay_event_length" "$replay_event"
    printf '{"type":"replay_recording","length":%s}\n%s' "$replay_recording_length" "$replay_recording"
  } | curl -fsS -X POST "$ingest_url" \
    -H "X-Sentry-Auth: $sentry_auth" --data-binary @- >/dev/null
  {
    printf '{"event_id":"%s"}\n' "$replay_id"
    printf '{"type":"replay_event","length":%s}\n%s\n' "$replay_event_length" "$replay_event"
    printf '{"type":"replay_recording","length":%s}\n%s' "$replay_recording_length" "$replay_recording"
  } | curl -fsS -X POST "$ingest_url" \
    -H "X-Sentry-Auth: $sentry_auth" --data-binary @- >/dev/null
  replay_event="{\"event_id\":\"$replay_id\",\"replay_id\":\"$replay_id\",\"segment_id\":1}"
  replay_recording=$(printf '{"segment_id":1}\n[]')
  replay_event_length=$(printf '%s' "$replay_event" | wc -c | tr -d ' ')
  replay_recording_length=$(printf '%s' "$replay_recording" | wc -c | tr -d ' ')
  {
    printf '{"event_id":"%s"}\n' "$replay_id"
    printf '{"type":"replay_event","length":%s}\n%s\n' "$replay_event_length" "$replay_event"
    printf '{"type":"replay_recording","length":%s}\n%s' "$replay_recording_length" "$replay_recording"
  } | curl -fsS -X POST "$ingest_url" \
    -H "X-Sentry-Auth: $sentry_auth" --data-binary @- >/dev/null
  replay_total=$(curl -fsS "$tracker_url/api/v1/apps/$app_id/replays/$replay_id" \
    -H "Authorization: Bearer $admin_token" | jq -er .total)
  [ "$replay_total" = 4 ]

  boundary="error-tracker-conformance-$run_id"
  {
    printf -- '--%s\r\n' "$boundary"
    printf 'Content-Disposition: form-data; name="upload_file_minidump"; filename="invalid.dmp"\r\n'
    printf 'Content-Type: application/octet-stream\r\n\r\n'
    printf 'not a minidump\r\n'
    printf -- '--%s--\r\n' "$boundary"
  } | curl -fsS -X POST "$tracker_url/api/$project_id/minidump/" \
    -H "X-Sentry-Auth: $sentry_auth" \
    -H "Content-Type: multipart/form-data; boundary=$boundary" \
    --data-binary @- >/dev/null
  has_failed_crash_event "$app_id"

  token_response=$(curl -fsS "$tracker_url/api/v1/tokens" \
    -H "Authorization: Bearer $admin_token" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"Conformance reader\",\"role\":\"read\",\"appId\":\"$app_id\"}")
  public_token=$(printf '%s' "$token_response" | jq -er .token)
  visible_apps=$(curl -fsS "$tracker_url/public/api/v1/apps" \
    -H "Authorization: Bearer $public_token" | jq -er 'length')
  [ "$visible_apps" = 1 ]
  initialize=$(curl -fsS "$tracker_url/public/mcp" \
    -H "Authorization: Bearer $public_token" \
    -H 'Accept: application/json, text/event-stream' \
    -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"conformance","version":"1"}}}')
  printf '%s' "$initialize" | jq -e '.result.serverInfo.name == "error-tracker"' >/dev/null
  notification=$(curl -sS -w '\n%{http_code}' "$tracker_url/public/mcp" \
    -H "Authorization: Bearer $public_token" \
    -H 'Accept: application/json, text/event-stream' \
    -H 'Content-Type: application/json' \
    -H 'MCP-Protocol-Version: 2025-11-25' \
    -d '{"jsonrpc":"2.0","method":"ping"}')
  notification_status=$(printf '%s\n' "$notification" | tail -n 1)
  notification_body=$(printf '%s\n' "$notification" | sed '$d')
  [ "$notification_status" = 400 ]
  [ -z "$notification_body" ]

  printf 'PASS protocol\n'
}

while IFS="$(printf '\t')" read -r image case_name; do
  case "$image" in ''|'#'*) continue ;; esac
  run_case "$image" "$case_name"
done < "$root/cases.tsv"

if [ -z "$selected_case" ] || [ "$selected_case" = protocol ]; then
  run_protocol_checks
fi
