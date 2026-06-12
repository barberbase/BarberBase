# staff-pwa-push Specification

## Purpose
TBD - created by archiving change implement-f5-staff-pwa. Update Purpose after archive.
## Requirements
### Requirement: Service Worker Scope Security
The system MUST register the Service Worker with a scope of `/dashboard/` only. SvelteKit's default auto-registration MUST be disabled, and manual registration MUST only occur when the route pathname starts with `/dashboard` and the user has a confirmed StaffJWT session.

#### Scenario: Registration on non-dashboard route
- **WHEN** the browser navigates to `/q/status` or `/login`
- **THEN** the Service Worker is not registered.

#### Scenario: Registration on dashboard route
- **WHEN** the browser navigates to `/dashboard` with a confirmed StaffJWT session
- **THEN** the Service Worker is manually registered with the scope `/dashboard/`.

### Requirement: Lock Screen Queue Control
The Service Worker push handler MUST display the lock-screen notification with `silent: true`, `tag: 'barberbase-queue'`, and `requireInteraction: true`. Tapping "NEXT CLIENT" MUST send a background POST request to the push call-next API using the Push Action Token (PAT) and update the notification text dynamically based on the API response.

#### Scenario: Tap next client with 200 OK
- **WHEN** the barber taps the "NEXT CLIENT" action button and the server returns 200 OK with `waiting_arrived_count > 0`
- **THEN** the notification is updated to "✓ Called · {count} remaining", keeping the "NEXT CLIENT" button.

#### Scenario: Tap next client with 401 Unauthorized
- **WHEN** the barber taps the "NEXT CLIENT" action button and the server returns 401 Unauthorized
- **THEN** the notification is updated to "Session expired · Open dashboard to continue" and the "NEXT CLIENT" button is removed.

### Requirement: Push Permission Prompt Gating
The Svelte permission component SHALL only display the push permission prompt to staff members when the session count (tracked via `localStorage.getItem('bb_dash_session_count')`) is at least 2, notification permission is 'default', and push APIs are supported by the browser.

#### Scenario: First session prompt hidden
- **WHEN** the dashboard mounts with session count equal to 1
- **THEN** the permission prompt is not displayed.

#### Scenario: Second session prompt shown
- **WHEN** the dashboard mounts with session count greater than or equal to 2 and notification permission is 'default'
- **THEN** the permission prompt is displayed.

### Requirement: Deny Path and Failure Graceful Degradation (Law 21)
The push permission prompt SHALL degrade gracefully if notification permission is denied, a subscription fails, or a POST subscription request fails. If `Notification.requestPermission()` returns `'denied'`, or if VAPID subscription throws an error, the prompt MUST close (`showPrompt = false`), swallow all errors silently, throw no exceptions, and allow the dashboard to function normally via SSE-only fallback.

#### Scenario: User denies notification permission
- **WHEN** the user taps "Enable Notifications" and `Notification.requestPermission()` returns `'denied'`
- **THEN** the prompt closes (`showPrompt = false`), no exceptions are thrown, and the dashboard operates normally via SSE.

#### Scenario: Post subscription network failure
- **WHEN** subscribing to push notifications fails during the backend API request
- **THEN** the error is caught and swallowed, the prompt closes (`showPrompt = false`), and the dashboard operates normally via SSE.

### Requirement: VAPID Key Configuration Source
The client application MUST retrieve the VAPID public key only from the SvelteKit static environment import (`PUBLIC_VAPID_PUBLIC_KEY` from `$env/static/public`). The application SHALL NOT fetch the public key via an API endpoint.

#### Scenario: Subscribing to push manager
- **WHEN** subscribing to the push manager
- **THEN** the application uses the VAPID public key imported directly from `$env/static/public`.

