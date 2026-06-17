#!/usr/bin/env bash
# =============================================================================
# v1.1-verify-queue.sh
# BarberBase V1.1 Android E2E — Queue State Verification Script
#
# Usage:
#   bash tests/e2e/v1.1-verify-queue.sh <queue_entry_id> <location_id>
#
# Arguments:
#   $1  queue_entry_id   UUID of the test entry that was waiting/arrived
#                        and should have been called by the NEXT CLIENT tap.
#   $2  location_id      UUID of the location under test.
#
# Environment:
#   DATABASE_URL         PostgreSQL connection string. Must be set before running.
#                        Example: postgres://user:pass@host:5432/barberbase
#
# Output:
#   PASS or FAIL per assertion, with raw values.
#   Exit code 0 = all assertions passed.
#   Exit code 1 = one or more assertions failed.
# =============================================================================

set -euo pipefail

# -----------------------------------------------------------------------------
# Argument validation
# -----------------------------------------------------------------------------

if [[ $# -lt 2 ]]; then
  echo "ERROR: Missing required arguments."
  echo "Usage: bash v1.1-verify-queue.sh <queue_entry_id> <location_id>"
  exit 1
fi

ENTRY_ID="$1"
LOCATION_ID="$2"

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "ERROR: DATABASE_URL environment variable is not set."
  echo "Export it before running: export DATABASE_URL='postgres://...'"
  exit 1
fi

# Validate UUID format (basic check: 8-4-4-4-12 hex groups)
UUID_REGEX='^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
if ! echo "$ENTRY_ID" | grep -qiE "$UUID_REGEX"; then
  echo "ERROR: queue_entry_id does not look like a UUID: $ENTRY_ID"
  exit 1
fi
if ! echo "$LOCATION_ID" | grep -qiE "$UUID_REGEX"; then
  echo "ERROR: location_id does not look like a UUID: $LOCATION_ID"
  exit 1
fi

# -----------------------------------------------------------------------------
# Helpers
# -----------------------------------------------------------------------------

FAIL_COUNT=0
PASS_COUNT=0

pass() {
  local label="$1"
  local value="$2"
  echo "[PASS] ${label}: ${value}"
  PASS_COUNT=$(( PASS_COUNT + 1 ))
}

fail() {
  local label="$1"
  local expected="$2"
  local actual="$3"
  echo "[FAIL] ${label}"
  echo "       Expected : ${expected}"
  echo "       Actual   : ${actual}"
  FAIL_COUNT=$(( FAIL_COUNT + 1 ))
}

# Run a single-column, single-row psql query. Returns trimmed output.
# Usage: psql_scalar "SELECT ..."
psql_scalar() {
  local query="$1"
  psql "$DATABASE_URL" --no-align --tuples-only --quiet -c "$query" 2>/dev/null | tr -d '[:space:]'
}

# Run a multi-column query and print a formatted table. Returns the raw output.
# Usage: psql_table "SELECT ..."
psql_table() {
  local query="$1"
  psql "$DATABASE_URL" --pset="border=2" --quiet -c "$query" 2>/dev/null
}

echo "============================================================"
echo " BarberBase V1.1 Queue Verification Script"
echo " Run time   : $(date '+%Y-%m-%d %H:%M:%S %Z')"
echo " entry_id   : ${ENTRY_ID}"
echo " location_id: ${LOCATION_ID}"
echo " DATABASE_URL: ${DATABASE_URL%%@*}@***"
echo "============================================================"
echo ""

# -----------------------------------------------------------------------------
# Assertion 1: queue_sessions — current queue_version for today
# -----------------------------------------------------------------------------
echo "--- Assertion 1: queue_sessions.queue_version (today's session) ---"

QV=$(psql_scalar "
  SELECT queue_version
  FROM queue_sessions
  WHERE location_id = '${LOCATION_ID}'
    AND business_date = CURRENT_DATE
  LIMIT 1;
")

echo "Raw: queue_version = ${QV}"

if [[ -z "$QV" ]]; then
  fail "queue_sessions row exists for today" "a row with queue_version >= 1" "NO ROW FOUND"
else
  # We assert the version is a positive integer (>= 1, meaning at least one mutation occurred)
  if [[ "$QV" =~ ^[0-9]+$ ]] && (( QV >= 1 )); then
    pass "queue_version is a positive integer (queue mutated)" "$QV"
  else
    fail "queue_version >= 1" ">= 1" "$QV"
  fi
fi

echo ""

# -----------------------------------------------------------------------------
# Assertion 2: Full queue_sessions row for context
# -----------------------------------------------------------------------------
echo "--- queue_sessions row (full, for reference) ---"
psql_table "
  SELECT id, location_id, business_date, status, last_token_number, queue_version,
         opened_at
  FROM queue_sessions
  WHERE location_id = '${LOCATION_ID}'
    AND business_date = CURRENT_DATE
  LIMIT 1;
"
echo ""

# -----------------------------------------------------------------------------
# Assertion 3: queue_entries — state and presence_state for the test entry
# -----------------------------------------------------------------------------
echo "--- Assertion 2: queue_entries state for entry_id = ${ENTRY_ID} ---"

ENTRY_STATE=$(psql_scalar "
  SELECT state FROM queue_entries WHERE id = '${ENTRY_ID}';
")

ENTRY_PRESENCE=$(psql_scalar "
  SELECT presence_state FROM queue_entries WHERE id = '${ENTRY_ID}';
")

ENTRY_CALLED_AT=$(psql_scalar "
  SELECT COALESCE(called_at::text, 'NULL') FROM queue_entries WHERE id = '${ENTRY_ID}';
")

echo "Raw: state         = ${ENTRY_STATE}"
echo "Raw: presence_state= ${ENTRY_PRESENCE}"
echo "Raw: called_at     = ${ENTRY_CALLED_AT}"

# State must be 'called' or 'in_progress' after NEXT CLIENT tap
if [[ "$ENTRY_STATE" == "called" ]] || [[ "$ENTRY_STATE" == "in_progress" ]]; then
  pass "entry state is called or in_progress" "$ENTRY_STATE"
else
  fail "entry state" "called or in_progress" "${ENTRY_STATE:-NO ROW FOUND}"
fi

# called_at must be non-null
if [[ "$ENTRY_CALLED_AT" == "NULL" ]] || [[ -z "$ENTRY_CALLED_AT" ]]; then
  fail "entry called_at is non-null" "a timestamp" "NULL"
else
  pass "entry called_at is non-null" "$ENTRY_CALLED_AT"
fi

# Presence state — after being called, it should be 'arrived' (presence does not change on call-next)
if [[ "$ENTRY_PRESENCE" == "arrived" ]]; then
  pass "entry presence_state is arrived" "$ENTRY_PRESENCE"
else
  # Not a hard failure — presence may transition in some configurations; record as warning
  echo "[WARN] entry presence_state expected 'arrived', got '${ENTRY_PRESENCE}' — verify manually"
fi

echo ""

# -----------------------------------------------------------------------------
# Assertion 4: Full queue_entries row for the test entry
# -----------------------------------------------------------------------------
echo "--- queue_entries row (full, for reference) ---"
psql_table "
  SELECT id, state, presence_state, is_dispatchable, token_number,
         called_at, started_at, completed_at, assigned_barber_id
  FROM queue_entries
  WHERE id = '${ENTRY_ID}';
"
echo ""

# -----------------------------------------------------------------------------
# Assertion 5: notification_events — most recent web_push row at this location
# -----------------------------------------------------------------------------
echo "--- Assertion 3: notification_events (most recent web_push at location) ---"

NE_CHANNEL=$(psql_scalar "
  SELECT channel
  FROM notification_events
  WHERE channel = 'web_push'
    AND location_id = '${LOCATION_ID}'
  ORDER BY created_at DESC
  LIMIT 1;
")

NE_TYPE=$(psql_scalar "
  SELECT notification_type
  FROM notification_events
  WHERE channel = 'web_push'
    AND location_id = '${LOCATION_ID}'
  ORDER BY created_at DESC
  LIMIT 1;
")

NE_STATUS=$(psql_scalar "
  SELECT status
  FROM notification_events
  WHERE channel = 'web_push'
    AND location_id = '${LOCATION_ID}'
  ORDER BY created_at DESC
  LIMIT 1;
")

NE_SOURCE_TYPE=$(psql_scalar "
  SELECT source_type
  FROM notification_events
  WHERE channel = 'web_push'
    AND location_id = '${LOCATION_ID}'
  ORDER BY created_at DESC
  LIMIT 1;
")

NE_CUSTOMER_ID=$(psql_scalar "
  SELECT COALESCE(customer_id::text, 'NULL')
  FROM notification_events
  WHERE channel = 'web_push'
    AND location_id = '${LOCATION_ID}'
  ORDER BY created_at DESC
  LIMIT 1;
")

echo "Raw: channel         = ${NE_CHANNEL}"
echo "Raw: notification_type = ${NE_TYPE}"
echo "Raw: status          = ${NE_STATUS}"
echo "Raw: source_type     = ${NE_SOURCE_TYPE}"
echo "Raw: customer_id     = ${NE_CUSTOMER_ID}"

if [[ "$NE_CHANNEL" == "web_push" ]]; then
  pass "notification_events.channel = 'web_push'" "$NE_CHANNEL"
else
  fail "notification_events.channel" "web_push" "${NE_CHANNEL:-NO ROW FOUND}"
fi

if [[ "$NE_TYPE" == "push_call_next" ]]; then
  pass "notification_events.notification_type = 'push_call_next'" "$NE_TYPE"
else
  fail "notification_events.notification_type" "push_call_next" "${NE_TYPE:-NO ROW FOUND}"
fi

if [[ "$NE_STATUS" == "sent" ]]; then
  pass "notification_events.status = 'sent'" "$NE_STATUS"
else
  fail "notification_events.status" "sent" "${NE_STATUS:-NO ROW FOUND}"
fi

if [[ "$NE_SOURCE_TYPE" == "staff_member" ]]; then
  pass "notification_events.source_type = 'staff_member'" "$NE_SOURCE_TYPE"
else
  fail "notification_events.source_type" "staff_member" "${NE_SOURCE_TYPE:-NO ROW FOUND}"
fi

# customer_id MUST be NULL for web_push rows (push is for staff, not customer)
if [[ "$NE_CUSTOMER_ID" == "NULL" ]] || [[ -z "$NE_CUSTOMER_ID" ]]; then
  pass "notification_events.customer_id = NULL (push is staff-targeted)" "NULL"
else
  fail "notification_events.customer_id" "NULL" "$NE_CUSTOMER_ID"
fi

echo ""

# -----------------------------------------------------------------------------
# Assertion 6: Full notification_events row for reference
# -----------------------------------------------------------------------------
echo "--- notification_events row (full, for reference) ---"
psql_table "
  SELECT id, channel, notification_type, source_type, source_id,
         status, customer_id, location_id, created_at
  FROM notification_events
  WHERE channel = 'web_push'
    AND location_id = '${LOCATION_ID}'
  ORDER BY created_at DESC
  LIMIT 1;
"
echo ""

# -----------------------------------------------------------------------------
# Assertion 7: push_enabled still true (410 Gone cleanup did NOT fire)
# -----------------------------------------------------------------------------
echo "--- Assertion 4: staff_members.push_enabled still true after dispatch ---"

# Derive staff_member_id from the notification_events source_id
STAFF_ID=$(psql_scalar "
  SELECT source_id
  FROM notification_events
  WHERE channel = 'web_push'
    AND location_id = '${LOCATION_ID}'
  ORDER BY created_at DESC
  LIMIT 1;
")

if [[ -z "$STAFF_ID" ]]; then
  fail "staff_member_id derivable from notification_events.source_id" "a UUID" "NOT FOUND"
else
  PUSH_ENABLED=$(psql_scalar "
    SELECT push_enabled FROM staff_members WHERE id = '${STAFF_ID}';
  ")
  ENDPOINT_SET=$(psql_scalar "
    SELECT (push_endpoint IS NOT NULL)::text FROM staff_members WHERE id = '${STAFF_ID}';
  ")

  echo "Raw: staff_member_id  = ${STAFF_ID}"
  echo "Raw: push_enabled     = ${PUSH_ENABLED}"
  echo "Raw: endpoint_is_set  = ${ENDPOINT_SET}"

  if [[ "$PUSH_ENABLED" == "t" ]] || [[ "$PUSH_ENABLED" == "true" ]]; then
    pass "staff_members.push_enabled = true (no 410 Gone cleanup)" "$PUSH_ENABLED"
  else
    fail "staff_members.push_enabled" "true" "${PUSH_ENABLED:-NO ROW FOUND}"
  fi

  if [[ "$ENDPOINT_SET" == "t" ]] || [[ "$ENDPOINT_SET" == "true" ]]; then
    pass "staff_members.push_endpoint IS NOT NULL" "true"
  else
    fail "staff_members.push_endpoint IS NOT NULL" "true" "${ENDPOINT_SET:-NULL or NOT FOUND}"
  fi
fi

echo ""

# -----------------------------------------------------------------------------
# Final summary
# -----------------------------------------------------------------------------
echo "============================================================"
echo " RESULTS"
echo " Passed: ${PASS_COUNT}"
echo " Failed: ${FAIL_COUNT}"
echo "============================================================"

if (( FAIL_COUNT == 0 )); then
  echo " OVERALL: PASS"
  exit 0
else
  echo " OVERALL: FAIL — ${FAIL_COUNT} assertion(s) failed. See [FAIL] lines above."
  exit 1
fi
