## Context

BarberBase requires an asynchronous webhook processing worker (`Processor`), message classifier, and intent resolver to handle incoming WhatsApp events. This implements unit C1.3.

Current state: Webhook ingress endpoints receive payloads from Bhejna and queue them in the `webhook_events` table (Mode A or Mode B).
Goal: Implement the background processor that claims these events, classifies them, and resolves customer check-ins or other message actions.

## Goals / Non-Goals

**Goals:**
- Implement a background loop in `internal/webhook/processor.go` using a PGX `SKIP LOCKED` query to safely claim events under multiple concurrent instances.
- Implement `internal/webhook/message_classifier.go` to parse text and interactive button payloads without DB calls.
- Implement `internal/webhook/intent_resolver.go` to run the multi-step JOIN transaction (including `queue_sessions` upsert-then-lock, duplicate check, variant snapshots, insertions into `visits`, `visit_services`, `queue_entries`, `outbox_events` and generating magic links).
- Implement `internal/repository/customer.go` to handle customer identification (with E.164 normalization, BSUID matching, and shadow profile generation).
- Implement `internal/domain/identity/resolver.go` to route queries to repository, and `internal/domain/identity/merge.go` to handle shadow profile updates.
- Adhere to critical system laws (Law 1, 2, 3, 7, 8, etc.).

**Non-Goals:**
- Integrating the worker into `cmd/server/main.go` (done in a future unit).
- Implementing Bhejna client HTTP sending or API endpoints.
- Implementing adjacent business workflows (C2.x or C3.x).

## Decisions

### 1. Webhook Worker Claim Logic
- Use a single database transaction for the claim query with `FOR UPDATE SKIP LOCKED`.
- Reclaim stuck processing rows (`locked_until < NOW()`) and retry failed rows (`attempts < 10`). Row is permanently excluded when `attempts >= 10`.
- Mark terminal failures and processing errors outside of the main transaction.

### 2. JOIN Intent Transaction Structure
- Lock `queue_sessions` using `FOR UPDATE` first before any mutations (Law 1).
- Sequence: Check if intent is valid -> Resolve or create customer (outside transaction) -> BEGIN main transaction -> Insert/Lock queue session -> Check duplicate entries -> Snapshot variants -> Insert visit -> Insert visit_services -> Insert queue_entry -> Increment token/version on queue session -> Resolve intent -> Insert outbox event (Law 7) -> COMMIT -> Broadcast SSE (Law 8).
- JOIN commands match location strictly via `token_code = $1` with exact comparison, avoiding wildcard matches on slugs (e.g. containing `%` or `_`).

### 3. Tenant ID Resolution for Buttons & Webhooks
- For button-payload events (`ActionOnTheWay`, `ActionCancel`, `ActionRatingButton`), we must NOT use the message sender or slug to resolve the tenant.
- Instead, query the referenced entity (such as `queue_entries` or `feedback_requests`) using the UUID suffix parsed from the payload to retrieve `tenant_id` and `location_id` directly, ensuring tenant integrity.

### 4. Customer Identity Routing
- Phone normalization strips non-digits and prepends '+'. Returns empty string if empty.
- Masked paths construct shadow profiles using `is_shadow_profile = true` and map the BSUID to `customer_identities`.
- Merging shadows promotes the profile in place (update phone/is_shadow_profile), returning nil on conflict with existing real profiles.

## Risks / Trade-offs

- [Risk] Concurrent check-ins for the same customer -> [Mitigation] Hard database unique constraint `idx_queue_entries_one_active_per_customer` and transaction-level pre-check.
- [Risk] Worker crash during processing -> [Mitigation] `locked_until` lease mechanism (30 seconds) allows another worker to pick up the event.

