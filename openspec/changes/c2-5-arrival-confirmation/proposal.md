## Why

Customers need a reliable, verified way to check in when they arrive physically at a salon location. This prevents customers from falsely claiming they have arrived when they are remote, ensures fairness in queue dispatching, and allows staff to override verification when necessary.

## What Changes

- Introduce a dedicated customer-facing arrival verification flow with support for:
  - Static PIN verification (bcrypt-hashed) with a 5-attempt rate limit per queue entry and 10/IP/hour rate limit.
  - GPS geolocation verification (checking that the customer is within the shop's defined arrival radius with <=150m accuracy).
  - NFC token verification (bcrypt-hashed).
- Implement a staff-only arrival override path that bypasses physical constraints but is fully logged.
- Implement customer self-declared `on_the_way` transition and `cancel` transition.
- Ensure all mutations strictly comply with serializable transaction locks, logging of attempts, and SSE broadcasts.

## Capabilities

### New Capabilities
- `arrival-confirmation`: Verification of customer physical arrival via PIN, GPS, or NFC, self-declared transit updates, customer-initiated cancellation, and staff overrides.

### Modified Capabilities

## Impact

- **Database**: Add query capabilities on `arrival_attempts`, `locations`, `queue_sessions`, and `queue_entries`.
- **API**: Implement endpoints in both customer-facing (`handlers_public.go`) and staff-facing (`handlers_staff.go`) APIs.
- **SSE**: Trigger real-time queue version updates to the staff dashboard.
