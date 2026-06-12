## Why

Provide staff members with a Progressive Web App (PWA) and Web Push Notification system for BarberBase. This allows barbers to manage the queue from a locked phone screen using a persistent control notification without needing to unlock their device or keep the browser open.

## What Changes

- Create PWA manifest for the staff dashboard scoped to `/dashboard/` to prevent intercepting customer-facing routes.
- Disable SvelteKit's automatic Service Worker registration.
- Implement a custom Service Worker under `/service-worker.js` to handle Web Push events and `notificationclick` actions using Push Action Tokens (PATs) instead of StaffJWTs.
- Implement the `PushPermissionPrompt.svelte` Svelte component using Svelte 5 runes for session-based and permission-based subscription prompting.
- Create SvelteKit Layout files to inject the manifest, slot, and push prompt component into `/dashboard/*`.
- Verify the system handles token expiration (401/403), double-taps (429), and network errors gracefully under Law 21 (fail-safe fallback).

## Capabilities

### New Capabilities
- `staff-pwa-push`: Web Push Notification support and progressive web app installability for the staff dashboard, including lock-screen actions and push subscription endpoint integration.

### Modified Capabilities
<!-- None -->

## Impact

- Affected Frontend Code: `svelte.config.js`, `static/dashboard/manifest.json`, `static/icons/icon-192.png`, `static/icons/icon-512.png`, `src/service-worker.js`, `src/lib/components/PushPermissionPrompt.svelte`, `src/routes/dashboard/+layout.svelte`, `src/routes/dashboard/+layout.server.ts`.
- Gated Service Worker registration only under `/dashboard/*` with StaffJWT confirmed (Law 17).
- Calls to `POST /v1/staff/push/subscribe` with authorization header and `POST /v1/staff/push/call-next` with `X-Push-Action-Token` header (Law 18).
