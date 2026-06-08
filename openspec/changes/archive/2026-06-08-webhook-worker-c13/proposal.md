## Why

BarberBase requires an asynchronous webhook processing pipeline to handle WhatsApp messages from Bhejna, classify user intents (such as joining the queue or cancelling spots), resolve and merge customer profiles (real, shadow, or masked), and securely broadcast queue state changes via SSE.

## What Changes

- Implement the inbound webhook worker loop using PGX SKIP LOCKED for concurrency-safe event consumption.
- Implement the WhatsApp message classifier for body-based and button-based payloads.
- Implement the check-in intent resolver transaction flow, capturing immutable visit services and creating visits/queue entries.
- Implement the customer repository with phone normalization (E.164) and shadow profile support.
- Implement the shadow profile merge logic.
- Implement default NoopBroadcaster for SSE updates.

## Capabilities

### New Capabilities
- `webhook-worker`: Asynchronous worker loop processing webhook events with concurrent claim logic.
- `message-classification`: Parsing and classification of incoming Bhejna webhooks into message actions.
- `intent-resolution`: Single-transaction JOIN resolution that creates visits, snapshots variant data, constructs queue entries, generates secure magic link tokens, and enqueues outbox notification events.
- `customer-identity`: Lookup and creation of real, shadow, and masked customer profiles, plus profile merge logic.

### Modified Capabilities

## Impact

- Database access routines via `pgxpool`.
- Webhook processor daemon background routine.
- Future integration in `cmd/server/main.go`.
