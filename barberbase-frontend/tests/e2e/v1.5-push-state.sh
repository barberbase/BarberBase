#!/usr/bin/env bash
# =============================================================================
# v1.5-push-state.sh
# BarberBase V1.5 Law 21 Regression — Push State Toggle Script
#
# Usage:
#   bash tests/e2e/v1.5-push-state.sh disable
#   bash tests/e2e/v1.5-push-state.sh enable
#
# Arguments:
#   disable   Set push_enabled=false and NULL all push subscription columns for
#             every active staff_member. Simulates zero push infrastructure.
#   enable    Set push_enabled=true for every active staff_member (flag only).
#             Does NOT restore push_endpoint/push_p256dh/push_auth — those were
#             NULLed by disable and must be re-seeded via the app or test fixture.
#
# Environment:
#   DATABASE_URL   PostgreSQL connection string. Must be set before running.
#                  Example: export DATABASE_URL='postgres://user:pass@host:5432/barberbase'
#
# Exit codes:
#   0   Success
#   1   Unknown argument or DATABASE_URL not set
# =============================================================================

set -euo pipefail

# -----------------------------------------------------------------------------
# Validate environment
# -----------------------------------------------------------------------------

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "ERROR: DATABASE_URL is not set."
  echo "Export it before running: export DATABASE_URL='postgres://...'"
  exit 1
fi

# -----------------------------------------------------------------------------
# Validate argument
# -----------------------------------------------------------------------------

if [[ $# -ne 1 ]]; then
  echo "ERROR: Expected exactly one argument: disable or enable"
  echo "Usage: bash v1.5-push-state.sh disable|enable"
  exit 1
fi

ACTION="$1"

# -----------------------------------------------------------------------------
# Helpers
# -----------------------------------------------------------------------------

# Run a psql command and return its output.
# psql -c returns "UPDATE N" on a successful UPDATE; we extract N.
psql_update() {
  local query="$1"
  psql "$DATABASE_URL" --no-align --tuples-only --quiet -c "$query" 2>&1
}

# Extract the row count from psql UPDATE output ("UPDATE N").
extract_row_count() {
  local psql_output="$1"
  echo "$psql_output" | grep -oE '[0-9]+$' | tail -1
}

# -----------------------------------------------------------------------------
# disable
# Sets push_enabled=false and NULLs all push subscription columns.
# This puts the database into the worst-case push-absence state:
#   - C6.6 EXISTS gate in CompleteVisitAndCheckout returns false
#   - Zero web_push.send outbox events will be inserted during checkout
#   - No push notifications will be dispatched by the outbox worker
# -----------------------------------------------------------------------------

if [[ "$ACTION" == "disable" ]]; then
  RESULT=$(psql "$DATABASE_URL" --quiet -c "
    UPDATE staff_members
    SET push_enabled  = false,
        push_endpoint = NULL,
        push_p256dh   = NULL,
        push_auth     = NULL
    WHERE is_active = true;
  " 2>&1)

  ROW_COUNT=$(echo "$RESULT" | grep -oE '[0-9]+' | tail -1)

  if [[ -z "$ROW_COUNT" ]]; then
    echo "ERROR: psql command failed or returned unexpected output:"
    echo "$RESULT"
    exit 1
  fi

  echo "Push disabled: ${ROW_COUNT} rows affected."
  echo ""
  echo "Database state applied:"
  echo "  push_enabled  = false"
  echo "  push_endpoint = NULL"
  echo "  push_p256dh   = NULL"
  echo "  push_auth     = NULL"
  echo "  WHERE is_active = true (${ROW_COUNT} staff_members rows)"
  echo ""
  echo "Next steps for full push-disabled configuration:"
  echo "  1. Unset backend env: VAPID_PUBLIC_KEY=\"\" VAPID_PRIVATE_KEY=\"\" VAPID_SUBJECT=\"\""
  echo "  2. Restart barberbase-core so the env change takes effect."
  echo "  3. Unset frontend env: PUBLIC_VAPID_PUBLIC_KEY=\"\""
  echo "  4. Verify:"
  echo "     psql \"\$DATABASE_URL\" -c \"SELECT COUNT(*) FROM staff_members WHERE push_enabled=true AND is_active=true;\""
  echo "     Expected: 0"
  exit 0
fi

# -----------------------------------------------------------------------------
# enable
# Restores push_enabled=true for every active staff_member.
# NOTE: push_endpoint, push_p256dh, push_auth are NOT restored — they were
# NULLed by disable and cannot be recovered from this script. Staff must
# re-subscribe via the browser (POST /v1/staff/push/subscribe) or the test
# fixture must re-seed the subscription columns.
# -----------------------------------------------------------------------------

if [[ "$ACTION" == "enable" ]]; then
  RESULT=$(psql "$DATABASE_URL" --quiet -c "
    UPDATE staff_members
    SET push_enabled = true
    WHERE is_active = true;
  " 2>&1)

  ROW_COUNT=$(echo "$RESULT" | grep -oE '[0-9]+' | tail -1)

  if [[ -z "$ROW_COUNT" ]]; then
    echo "ERROR: psql command failed or returned unexpected output:"
    echo "$RESULT"
    exit 1
  fi

  echo "Push re-enabled: ${ROW_COUNT} rows affected. Note: subscription data (endpoint/p256dh/auth) was cleared. Re-subscribe via the app."
  echo ""
  echo "IMPORTANT: push_endpoint, push_p256dh, and push_auth are still NULL."
  echo "The push_enabled flag is true but no push notifications will be sent"
  echo "until staff re-subscribe via the dashboard (POST /v1/staff/push/subscribe)"
  echo "or the test fixture re-seeds these columns."
  echo ""
  echo "Next steps:"
  echo "  1. Restore backend env: VAPID_PUBLIC_KEY=<key> VAPID_PRIVATE_KEY=<key> VAPID_SUBJECT=<uri>"
  echo "  2. Restart barberbase-core."
  echo "  3. Restore frontend env: PUBLIC_VAPID_PUBLIC_KEY=<key>"
  echo "  4. Re-seed test push subscription data via the app or test fixture."
  exit 0
fi

# -----------------------------------------------------------------------------
# Unknown argument
# -----------------------------------------------------------------------------

echo "ERROR: Unknown argument: '${ACTION}'"
echo "Valid arguments: disable | enable"
echo "Usage: bash v1.5-push-state.sh disable|enable"
exit 1
