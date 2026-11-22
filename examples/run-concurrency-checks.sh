#!/usr/bin/env bash
set -euo pipefail

# Concurrency smoke & stress checks against a running examples compose stack
# (`docker compose -f examples/compose.yaml up --build`).
#
# Tunables:
#   TARGET_URL               Base Envoy listener URL (default: http://localhost:8080)
#   RUN_CLIENT_TIMEOUT_CHECK Set to 1 to run the optional Check 5 observing Envoy's
#                            mid-upstream io.EOF stream close behavior.

if ! command -v hey >/dev/null 2>&1; then
  echo "error: 'hey' is required (e.g. brew install hey)" >&2
  exit 1
fi

TARGET_URL="${TARGET_URL:-http://localhost:8080}"
POST_PAYLOAD='{"upstream":"upstream","maskme":"secret"}'

banner() {
  echo "---------------------------------------------------------------------------"
  echo "$1"
  echo "Expected: $2"
  echo "---------------------------------------------------------------------------"
  echo ""
}

# Check 1: Single-wave deduplication (1 winner + 49 concurrent duplicates).
# Payload exercises masker and router alongside digest/dedup; avoid /echo/...
# because the echo filter short-circuits before reaching upstream.
banner \
  "1. Single-wave check (${TARGET_URL}/items)..." \
  "[200] 1 responses and [409] 49 responses"

hey -n 50 -c 50 -m POST \
  -T "application/json" \
  -H "X-Echo-Delay: 0.5" \
  -d "${POST_PAYLOAD}" \
  "${TARGET_URL}/items"

# Check 2: Sustained map-contention churn (concurrent read + write + delete).
# Note: dedup uses failure_mode_allow: true in examples/envoy.yaml, so an
# unguarded map panic would cause Envoy to fail open into 200s. Confirm zero
# panics afterwards with: docker compose -f examples/compose.yaml logs dedup
banner \
  "2. Sustained map-contention check (${TARGET_URL}/items)..." \
  "[200] ~70-100 responses (~1 per 50ms), [409] ~10x-100x responses, 0 x 5xx"

hey -z 5s -c 50 -m POST \
  -T "application/json" \
  -H "X-Echo-Delay: 0.05" \
  -d "${POST_PAYLOAD}" \
  "${TARGET_URL}/items"

# Check 3: Post-contention slot release check.
# Confirms the final winner from Check 2 unlocked /items cleanly without
# leaking or leaving stale inFlight entries after concurrent churn.
sleep 0.2
banner \
  "3. Post-contention release verification (${TARGET_URL}/items)..." \
  "[200] 1 responses"

hey -n 1 -c 1 -m POST \
  -T "application/json" \
  -d "${POST_PAYLOAD}" \
  "${TARGET_URL}/items"

# Check 4: Concurrent read-only pass-through.
# GET/HEAD/OPTIONS/DELETE are excluded by dedupable(ctx), so all requests
# should hash concurrently and reach upstream with zero 409 conflicts.
banner \
  "4. Concurrent read-only GET check (${TARGET_URL}/items)..." \
  "[200] 50 responses and 0 x [409]"

hey -n 50 -c 50 -m GET \
  -H "X-Echo-Delay: 0.2" \
  "${TARGET_URL}/items"

# Check 5 (Optional): Downstream timeout mid-upstream and Envoy io.EOF note.
#
# When a client times out (-t 1) while Envoy is waiting on upstream (X-Echo-Delay: 3),
# the ext_proc filter has already returned ContinueRequest() for RequestBody and is
# idle in srv.Recv() awaiting ResponseHeaders. On downstream disconnect in that
# state, Envoy closes the idle gRPC stream cleanly (io.EOF) rather than sending
# RST_STREAM (codes.Canceled). Because the SDK treats io.EOF as normal stream EOF
# (skipping ErrorHandler) and ResponseHeaders never arrives, dedup never observes
# completion for that request key -- leaving that per-run path returning 409 until
# the dedup container restarts (unless dedup uses a TTL or an SDK stream-end hook).
if [[ "${RUN_CLIENT_TIMEOUT_CHECK:-0}" == "1" ]]; then
  timeout_path="/orders-$(date +%s)"
  banner \
    "5. Mid-upstream client timeout observation (${TARGET_URL}${timeout_path})..." \
    "1 timeout error + [409] 9 responses; follow-up likely returns [409] due to Envoy idle io.EOF teardown"

  set +e
  hey -n 10 -c 10 -t 1 -m POST \
    -T "application/json" \
    -H "X-Echo-Delay: 3" \
    -d '{"upstream":"upstream"}' \
    "${TARGET_URL}${timeout_path}"
  set -e

  sleep 4
  hey -n 1 -c 1 -m POST \
    -T "application/json" \
    -d '{"upstream":"upstream"}' \
    "${TARGET_URL}${timeout_path}"
fi
