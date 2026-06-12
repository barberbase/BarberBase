## Context

This design outlines the technical approach to implementing the Progressive Web App (PWA) manifest and Service Worker push / interaction handling on the BarberBase staff dashboard.

## Goals / Non-Goals

**Goals:**
- Implement the progressive web app manifest with scope restricted strictly to `/dashboard/`.
- Disable default SvelteKit root-scope Service Worker registration.
- Build a custom service worker to process push events and handle background queue advance actions using Push Action Tokens (PAT).
- Build Svelte 5 `PushPermissionPrompt` component which manages session counting and triggers manual registration and push subscription.
- Add layouts to pass authentication token context from cookies to Svelte layouts.

**Non-Goals:**
- Support for multiple active devices per staff member (one subscription per staff member row on backend, latest wins).
- Integrating push/PWA code into customer-facing pages (WhatsApp webviews).
- Support for background audio playback or MediaSession API within the service worker context.

## Decisions

### 1. Scoped Service Worker Registration
We will disable SvelteKit's built-in service worker registration to prevent interception of requests at the root scope `/`. Intercepting root-scope requests conflicts with Law 17 by interfering with customer-facing WhatsApp webview pages. Instead, we will register `/service-worker.js` with `{ scope: '/dashboard/' }` directly from the `PushPermissionPrompt.svelte` component when staff consent is obtained.

### 2. PushActionToken (PAT) for Background Invocation
The service worker uses `X-Push-Action-Token` for authorization when posting to `/staff/push/call-next` rather than a standard StaffJWT. This ensures the background task succeeds even if the Svelte session is expired (TTL 15 min vs haircut duration 20–45 min).

### 3. Dynamic Notification Updates (Failure & Rate Limiting)
Every network response or failure from the call-next action must update the active notification body to prevent the barber from acting on stale success assumptions. To prevent double-clicks from corrupting queue state, HTTP 429 status will bypass updates and return immediately.

### 4. VAPID Key Source
The VAPID public key must be resolved purely client-side from the SvelteKit static environment import (`PUBLIC_VAPID_PUBLIC_KEY` from `$env/static/public`) rather than executing an API fetch call. This simplifies page loading and prevents network dependency/latency on prompt rendering.

### 5. Silent Fallback on Subscription Deny or Failure
Under Law 21, push notification features are non-critical convenience enhancements. Therefore, if permission is denied, or if registration/subscription APIs fail, the prompt component must silently close (`showPrompt = false`), swallow all errors/exceptions, and ensure the core dashboard application behaves normally using standard SSE.

## Risks / Trade-offs

- **[Risk] FCM/APNs endpoint changes or expires** → Mitigation: Go backend handles HTTP 410 Gone by disabling the subscription on the staff member's DB row and logging the failure.
- **[Risk] Multiple refreshes over-counting dashboard sessions** → Mitigation: Two-layer tracking using `sessionStorage` (`bb_dash_session_marked`) and `localStorage` (`bb_dash_session_count`) ensures a reload in the same tab does not trigger prompt prematurely.
