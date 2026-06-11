## Why

Trigger web push notifications to staff when a visit checkout completes, ensuring that staff members are notified of checkout events in real time.

## What Changes

- Add a conditional check during the checkout transaction to determine if there are any active, push-enabled staff members at the location.
- Insert a `web_push.send` outbox event inside the checkout transaction if push-enabled staff exist, ensuring atomicity.
- Ensure checkout proceeds normally without outbox insertion if no push-enabled staff exist.

## Capabilities

### New Capabilities

### Modified Capabilities

- `visit-checkout`: Add requirement to insert `web_push.send` outbox event on checkout completion when active, push-enabled staff exist.

## Impact

- Modifies checkout transaction logic in `internal/domain/queue/commands.go`.
- Relies on `staff_members` table status/configuration and `outbox_events` table for web push notification events.
