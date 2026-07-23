import { expect, test } from '@playwright/test';

// /platform/devices sits under src/routes/platform/+layout.server.ts, which
// redirects to /platform/login whenever platform.env.PLATFORM_ADMIN_KEY is
// unset (see that file). The e2e harness runs `npm run build && npm run
// preview` (see playwright.config.ts) with no Cloudflare platform bindings,
// so PLATFORM_ADMIN_KEY is never set here — same constraint every other spec
// under src/routes/platform hits. That makes the auth gate the only thing
// this suite can exercise without a live backend.
//
// The full happy path (load devices → create device → one-time secret shown
// → add button → toggle active) requires:
//   - PLATFORM_ADMIN_KEY set on the preview platform env (wrangler pages dev
//     with --binding, or a .dev.vars file), AND
//   - a running barberbase-core API at PUBLIC_API_BASE serving
//     /v1/admin/devices, /v1/admin/devices/{id}, /v1/admin/devices/{id}/buttons.
// Run it manually against a local `make build && ./bin/server` + seeded
// tenant/location, or add it as a separate integration job once the harness
// supports platform bindings — do not fake it with an in-process http mock,
// since the auth gate itself can't be bypassed from inside a route action.

test('unauthenticated access to /platform/devices redirects to the operator login', async ({
	page
}) => {
	const response = await page.goto('/platform/devices');
	await expect(page).toHaveURL(/\/platform\/login$/);
	expect(response?.status()).toBeLessThan(400);
	await expect(page.getByRole('heading', { name: /BarberBase/i })).toBeVisible();
});

test('unauthenticated access to /platform/devices with query params still redirects', async ({
	page
}) => {
	await page.goto('/platform/devices?location_id=loc-123&tenant_id=tenant-1');
	await expect(page).toHaveURL(/\/platform\/login$/);
});
