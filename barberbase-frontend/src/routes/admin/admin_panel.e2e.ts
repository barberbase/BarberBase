import { expect, test } from '@playwright/test';
import http from 'http';

let server: http.Server;
const mockPort = 9090;

// JWT for owner role: {"role":"owner","exp":9999999999,"location_id":"loc-123","staff_member_id":"staff-1","name":"Test Owner","tenant_id":"tenant-1"}
const OWNER_JWT =
	'dummy.eyJyb2xlIjoib3duZXIiLCJleHAiOjk5OTk5OTk5OTksImxvY2F0aW9uX2lkIjoibG9jLTEyMyIsInN0YWZmX21lbWJlcl9pZCI6InN0YWZmLTEiLCJuYW1lIjoiVGVzdCBPd25lciIsInRlbmFudF9pZCI6InRlbmFudC0xIn0.dummy';

// JWT for barber role: {"role":"barber","exp":9999999999,"location_id":"loc-123","staff_member_id":"barber-1","name":"Test Barber","tenant_id":"tenant-1"}
const BARBER_JWT =
	'dummy.eyJyb2xlIjoiYmFyYmVyIiwiZXhwIjo5OTk5OTk5OTk5LCJsb2NhdGlvbl9pZCI6ImxvYy0xMjMiLCJzdGFmZl9tZW1iZXJfaWQiOiJiYXJiZXItMSIsIm5hbWUiOiJUZXN0IEJhcmJlciIsInRlbmFudF9pZCI6InRlbmFudC0xIn0.dummy';

let lastShopStatusBody: any = null;
let connectWhatsAppBody: any = null;
let lastCreateServiceBody: any = null;

test.beforeAll(() => {
	server = http.createServer((req, res) => {
		res.setHeader('Access-Control-Allow-Origin', '*');
		res.setHeader('Access-Control-Allow-Methods', 'GET, POST, PATCH, PUT, DELETE, OPTIONS');
		res.setHeader('Access-Control-Allow-Headers', 'Authorization, Content-Type, Cookie');
		res.setHeader('Access-Control-Allow-Credentials', 'true');

		if (req.method === 'OPTIONS') {
			res.writeHead(200);
			res.end();
			return;
		}

		const url = req.url || '';

		// Auth refresh
		if (url.includes('/auth/staff/refresh')) {
			res.writeHead(401);
			res.end();
			return;
		}

		// Services catalog
		if (req.method === 'GET' && url.includes('/admin/locations/') && url.includes('/services')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(
				JSON.stringify({
					location_id: 'loc-123',
					display_mode: 'hierarchical',
					categories: [
						{
							id: 'cat-1',
							name: 'Hair',
							gender: 'men',
							sort_order: 1,
							groups: [
								{
									id: 'grp-1',
									name: 'Fade',
									variants: [
										{
											id: 'var-1',
											name: 'Mid Fade',
											duration_minutes: 30,
											price_paise: 15000,
											allow_walk_in: true,
											allow_appointment: true,
											requires_appointment: false,
											is_popular: true
										}
									]
								}
							]
						}
					]
				})
			);
			return;
		}

		// Create service variant
		if (req.method === 'POST' && url.includes('/admin/locations/') && url.includes('/services')) {
			let body = '';
			req.on('data', (chunk) => (body += chunk));
			req.on('end', () => {
				try {
					lastCreateServiceBody = JSON.parse(body);
				} catch {
					lastCreateServiceBody = null;
				}
				res.writeHead(201, { 'Content-Type': 'application/json' });
				res.end(
					JSON.stringify({ id: 'var-new', name: 'Test', duration_minutes: 30, price_paise: 15000 })
				);
			});
			return;
		}

		// Update service variant
		if (req.method === 'PATCH' && url.includes('/admin/locations/') && url.includes('/services/')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({}));
			return;
		}

		// Staff members
		if (req.method === 'GET' && url.includes('/staff/members')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(
				JSON.stringify({
					staff: [
						{ id: 'barber-1', name: 'Alice', role: 'barber', status: 'idle' },
						{ id: 'manager-1', name: 'Bob', role: 'manager', status: 'idle' }
					]
				})
			);
			return;
		}

		// Add staff member
		if (req.method === 'POST' && url.includes('/admin/staff')) {
			res.writeHead(201);
			res.end();
			return;
		}

		// Shop status GET
		if (req.method === 'GET' && url.includes('/staff/shop/status')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(
				JSON.stringify({
					shop_status: 'open',
					queue_session_status: 'active',
					manual_override_active: false,
					override_expires_at: null,
					arrival_pin: '7382'
				})
			);
			return;
		}

		// Shop status PATCH
		if (req.method === 'PATCH' && url.includes('/staff/shop/status')) {
			let body = '';
			req.on('data', (chunk) => (body += chunk));
			req.on('end', () => {
				try {
					lastShopStatusBody = JSON.parse(body);
				} catch {
					lastShopStatusBody = null;
				}
				// Return 422 when no modal_action to trigger the modal
				if (!lastShopStatusBody?.modal_action && lastShopStatusBody?.status === 'closed') {
					res.writeHead(422, { 'Content-Type': 'application/json' });
					res.end(
						JSON.stringify({
							code: 'ACTIVE_ENTRIES_EXIST',
							message: 'Active entries exist',
							active_entry_count: 3
						})
					);
				} else {
					res.writeHead(200);
					res.end();
				}
			});
			return;
		}

		// WhatsApp connect
		if (req.method === 'POST' && url.includes('/whatsapp/connect')) {
			let body = '';
			req.on('data', (chunk) => (body += chunk));
			req.on('end', () => {
				try {
					connectWhatsAppBody = JSON.parse(body);
				} catch {
					connectWhatsAppBody = null;
				}
				res.writeHead(200, { 'Content-Type': 'application/json' });
				res.end(
					JSON.stringify({
						whatsapp_mode: 'own_number',
						webhook_url: `https://api.barberbase.in/v1/webhooks/bhejna/loc/loc-123`
					})
				);
			});
			return;
		}

		// WhatsApp disconnect
		if (req.method === 'POST' && url.includes('/whatsapp/disconnect')) {
			res.writeHead(204);
			res.end();
			return;
		}

		// Arrival PIN regenerate
		if (req.method === 'POST' && url.includes('/arrival-pin/regenerate')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ new_pin: '9999' }));
			return;
		}

		// Analytics
		if (req.method === 'GET' && url.includes('/staff/analytics/daily')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(
				JSON.stringify({
					business_date: '2026-06-11',
					total_visits: 12,
					total_revenue_paise: 180000,
					average_wait_minutes: 18,
					no_show_count: 1,
					cancelled_count: 0,
					barber_breakdown: [
						{
							barber_id: 'barber-1',
							barber_name: 'Alice',
							visits_completed: 7,
							revenue_paise: 105000,
							average_service_minutes: 28
						},
						{
							barber_id: 'barber-2',
							barber_name: 'Bob',
							visits_completed: 5,
							revenue_paise: 75000,
							average_service_minutes: 32
						}
					]
				})
			);
			return;
		}

		// SSE stream
		if (url.includes('/v1/stream/')) {
			res.writeHead(200, {
				'Content-Type': 'text/event-stream',
				'Cache-Control': 'no-cache',
				Connection: 'keep-alive'
			});
			res.write(':\n\n');
			return;
		}

		// Queue snapshot (for dashboard redirect target)
		if (url.includes('/staff/queue/snapshot')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ queue_version: 1, session_status: 'active', entries: [] }));
			return;
		}

		// Public service catalog (for dashboard)
		if (url.includes('/public/locations/') && url.includes('/service-catalog')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ categories: [] }));
			return;
		}

		res.writeHead(404);
		res.end('{}');
	});

	server.listen(mockPort);
});

test.afterAll(() => {
	server.close();
});

// Helper to set owner JWT cookie
async function setOwnerCookie(context: any) {
	await context.addCookies([
		{
			name: 'access_token',
			value: OWNER_JWT,
			domain: 'localhost',
			path: '/'
		}
	]);
}

// Helper to set barber JWT cookie
async function setBarberCookie(context: any) {
	await context.addCookies([
		{
			name: 'access_token',
			value: BARBER_JWT,
			domain: 'localhost',
			path: '/'
		}
	]);
}

// TEST 1: Barber role JWT → redirected to /dashboard
test('barber role at /admin redirects to /dashboard', async ({ page, context }) => {
	await setBarberCookie(context);
	await page.goto('/admin');
	// Should land on /dashboard, not /admin
	await expect(page).toHaveURL(/\/dashboard/);
});

// TEST 2: Mode B paste → submit → webhook_url visible in response panel
test('Mode B paste → submit → webhook_url visible', async ({ page, context }) => {
	await setOwnerCookie(context);
	await page.goto('/admin/whatsapp');

	const configJson = JSON.stringify({
		bhejna_config_version: '1',
		phone_number: '+912212345678',
		api_key: 'nxt_live_test',
		webhook_secret: 'whsec_test',
		whatsapp_status: 'ACTIVE'
	});

	const textarea = page.locator('#config-json-input');
	await expect(textarea).toBeVisible();
	await textarea.fill(configJson);

	await page.locator('#submit-connect-whatsapp-btn').click();

	// After submit, webhook_url panel should be visible
	await expect(page.locator('#webhook-url-panel')).toBeVisible({ timeout: 8000 });
	await expect(page.locator('#webhook-url-display')).toHaveValue(/webhooks\/bhejna\/loc/);
});

// TEST 3: Create service variant with price 150 → backend receives price_paise: 15000
test('create service variant price 150 → price_paise: 15000 in request body', async ({
	page,
	context
}) => {
	lastCreateServiceBody = null;
	await setOwnerCookie(context);
	await page.goto('/admin/services');

	// Open create form
	await page.locator('#toggle-create-form-btn').click();
	await expect(page.locator('#create-service-form')).toBeVisible();

	await page.locator('#category_name').fill('Hair');
	await page.locator('#group_name').fill('Test Group');
	await page.locator('#variant_name').fill('Test Cut');
	await page.locator('#duration_minutes').fill('30');
	await page.locator('#price_rupees').fill('150');

	// SvelteKit handles form submission server-side, so we wait for response
	// and then check our mock server received price_paise: 15000
	await page.locator('#submit-create-service-btn').click();

	// Wait until the mock server received the request (give it up to 5 seconds)
	await page.waitForTimeout(2000);

	// The mock server should have received the body with price_paise: 15000
	expect(lastCreateServiceBody?.price_paise).toBe(15000);
});

// TEST 4: Analytics page shows ₹ prefix, not raw paise
test('analytics page shows ₹ prefix not raw integer', async ({ page, context }) => {
	await setOwnerCookie(context);
	await page.goto('/admin/analytics');

	// Total revenue card should show ₹1,800 (180000 paise / 100)
	const revenueCard = page.locator('#analytics-total-revenue');
	await expect(revenueCard).toBeVisible({ timeout: 8000 });
	const revenueText = await revenueCard.textContent();
	expect(revenueText).toContain('₹');
	expect(revenueText).not.toContain('180000'); // raw paise should NOT appear

	// Barber revenue cells should also show ₹
	const barberRevenue = page.locator('#barber-revenue-cell').first();
	await expect(barberRevenue).toBeVisible();
	const barberText = await barberRevenue.textContent();
	expect(barberText).toContain('₹');
	expect(barberText).not.toContain('105000');
});

// TEST 5: Shop status 422 → modal appears; selecting action re-submits with modal_action
test('shop status 422 → modal with two action buttons; selecting re-submits with modal_action', async ({
	page,
	context
}) => {
	lastShopStatusBody = null;
	await setOwnerCookie(context);
	await page.goto('/admin/shop');

	// The radio inputs are sr-only, so click the containing label div instead
	const closedLabel = page.locator('label:has(input[name="status"][value="closed"])');
	await expect(closedLabel).toBeVisible({ timeout: 8000 });
	await closedLabel.click();

	await page.locator('#submit-shop-status-btn').click();

	// Modal should appear after 422 response
	const modal = page.locator('#shop-status-conflict-modal');
	await expect(modal).toBeVisible({ timeout: 8000 });

	// Both action buttons should be present
	await expect(page.locator('#modal-finish-remaining-btn')).toBeVisible();
	await expect(page.locator('#modal-expire-remaining-btn')).toBeVisible();

	// Click "finish_remaining" — this triggers another PATCH to our mock server
	await page.locator('#modal-finish-remaining-btn').click();

	// Wait for mock server to receive the request
	await page.waitForTimeout(2000);

	// The mock should now have received modal_action: 'finish_remaining'
	expect(lastShopStatusBody?.modal_action).toBe('finish_remaining');
});
