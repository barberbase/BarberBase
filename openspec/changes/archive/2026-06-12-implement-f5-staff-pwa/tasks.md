## 1. PWA configuration

- [x] 1.1 Add serviceWorker register false config to svelte.config.js
- [x] 1.2 Create static/dashboard/manifest.json with dashboard-restricted scope and standalone display
- [x] 1.3 Create dark background block PNG icons at static/icons/icon-192.png and static/icons/icon-512.png

## 2. Service Worker Implementation

- [x] 2.1 Implement src/service-worker.js with push event listener mapping Go backend payload fields
- [x] 2.2 Implement notificationclick handler in src/service-worker.js containing the response status logic for 200, 404, 401/403, 429, 5xx and network error

## 3. UI Component and Routes Integration

- [x] 3.1 Create src/lib/components/PushPermissionPrompt.svelte with session counter logic and push subscription POST handler. Retrieve VAPID key exclusively from $env/static/public (no API fetch) and gracefully handle Notification.requestPermission() === 'denied' or sub failures by closing the prompt (showPrompt = false) without throwing errors (Law 21)
- [x] 3.2 Create src/routes/dashboard/+layout.server.ts to extract access_token cookie
- [x] 3.3 Create src/routes/dashboard/+layout.svelte to load manifest and embed PushPermissionPrompt

## 4. Verification and Testing

- [x] 4.1 Build the SvelteKit project and run playwright/unit tests to verify compliance, including testing that the deny path throws no error and dashboard continues to work under Law 21
