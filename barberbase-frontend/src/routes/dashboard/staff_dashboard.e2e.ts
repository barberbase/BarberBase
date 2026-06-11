import { expect, test } from '@playwright/test';
import http from 'http';

let server: http.Server;
const mockPort = 9090;

let currentSnapshot = {
	queue_version: 1,
	session_status: 'active',
	entries: [
		{
			id: 'entry-1',
			token_number: 101,
			state: 'in_progress',
			presence_state: 'arrived',
			customer: {
				name: 'Jane Doe',
				visit_count: 3
			},
			services: [
				{
					name: 'Haircut',
					price_paise: 50000,
					duration_minutes: 30
				}
			]
		},
		{
			id: 'entry-2',
			token_number: 102,
			state: 'waiting',
			presence_state: 'arrived',
			customer: {
				name: 'John Smith',
				visit_count: 1
			},
			services: [
				{
					name: 'Shave',
					price_paise: 30000,
					duration_minutes: 15
				}
			]
		}
	]
};

let callNextRequests = 0;
let sseClients: http.ServerResponse[] = [];

test.beforeAll(() => {
	// Start a mock server to intercept SvelteKit backend API and SSE requests
	server = http.createServer((req, res) => {
		res.setHeader('Access-Control-Allow-Origin', '*');
		res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
		res.setHeader('Access-Control-Allow-Headers', 'Authorization, Content-Type, Cookie');
		res.setHeader('Access-Control-Allow-Credentials', 'true');

		if (req.method === 'OPTIONS') {
			res.writeHead(200);
			res.end();
			return;
		}

		const url = req.url || '';

		if (url.includes('/v1/stream/')) {
			res.writeHead(200, {
				'Content-Type': 'text/event-stream',
				'Cache-Control': 'no-cache',
				'Connection': 'keep-alive'
			});
			res.write(':\n\n'); // keep-alive comment
			sseClients.push(res);
			return;
		}

		if (url.includes('/v1/staff/queue/snapshot')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify(currentSnapshot));
			return;
		}

		if (url.includes('/v1/staff/members')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ staff: [{ id: 'barber-1', name: 'Alice' }] }));
			return;
		}

		if (url.includes('/service-catalog')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ categories: [] }));
			return;
		}

		if (url.includes('/v1/staff/queue/call-next')) {
			callNextRequests++;
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ id: 'entry-2' }));
			return;
		}

		if (url.includes('/complete')) {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ success: true }));
			return;
		}

		res.writeHead(404);
		res.end();
	});

	server.listen(mockPort);
});

test.afterAll(() => {
	server.close();
});

test('one-tap actions issue exactly one request (debounced)', async ({ page, context }) => {
	callNextRequests = 0;

	// Add access token to bypass redirect to login
	await context.addCookies([
		{
			name: 'access_token',
			value: 'dummy.eyJyb2xlIjoiYmFyYmVyIiwiZXhwIjo5OTk5OTk5OTk5LCJsb2NhdGlvbl9pZCI6ImxvYy0xMjMiLCJuYW1lIjoiVGVzdCBTdGFmZiJ9.dummy',
			domain: 'localhost',
			path: '/'
		}
	]);

	await page.goto('/dashboard');
	await expect(page.locator('text=CALL NEXT CLIENT')).toBeVisible();

	const button = page.locator('text=CALL NEXT CLIENT');
	await expect(button).toBeEnabled();

	// Click twice rapidly to test debouncing (force second click so Playwright doesn't auto-wait)
	await button.click();
	await button.click({ force: true }).catch(() => {});

	// Verify button is disabled immediately
	await expect(button).toBeDisabled();

	// Wait for debounce period (1 second) to expire
	await page.waitForTimeout(1500);

	// Verify it becomes enabled again
	await expect(button).toBeEnabled();

	// Verify exactly 1 request was sent to the mock server
	expect(callNextRequests).toBe(1);
});

test('checkout total mismatch blocks submit', async ({ page, context }) => {
	await context.addCookies([
		{
			name: 'access_token',
			value: 'dummy.eyJyb2xlIjoiYmFyYmVyIiwiZXhwIjo5OTk5OTk5OTk5LCJsb2NhdGlvbl9pZCI6ImxvYy0xMjMiLCJuYW1lIjoiVGVzdCBTdGFmZiJ9.dummy',
			domain: 'localhost',
			path: '/'
		}
	]);

	await page.goto('/dashboard');

	// Open the checkout modal
	await page.locator('text=Complete Service').click();
	await expect(page.locator('text=Complete Service & Checkout')).toBeVisible();

	const submitButton = page.locator('button:has-text("Complete Checkout")');
	await expect(submitButton).toBeEnabled();

	// Locate the payment amount input and set a mismatched value (e.g. ₹400 instead of ₹500 expected)
	const amountInput = page.locator('input[id^="payment-amount-"]');
	await amountInput.fill('400');

	// Assert submit button is disabled
	await expect(submitButton).toBeDisabled();

	// Assert mismatch error is displayed immediately in UI
	await expect(page.locator('text=Payment mismatch: Entered payment lines')).toBeVisible();

	// Correct the mismatch by entering ₹500
	await amountInput.fill('500');

	// Assert submit button becomes enabled again and error is cleared
	await expect(submitButton).toBeEnabled();
	await expect(page.locator('text=Payment mismatch: Entered payment lines')).not.toBeVisible();
});

test('kill SSE, mutate via another client, dashboard recovers on reconnect via snapshot', async ({
	page,
	context
}) => {
	// Reset snapshot
	currentSnapshot = {
		queue_version: 1,
		session_status: 'active',
		entries: [
			{
				id: 'entry-1',
				token_number: 101,
				state: 'in_progress',
				presence_state: 'arrived',
				customer: {
					name: 'Jane Doe',
					visit_count: 3
				},
				services: [
					{
						name: 'Haircut',
						price_paise: 50000,
						duration_minutes: 30
					}
				]
			},
			{
				id: 'entry-2',
				token_number: 102,
				state: 'waiting',
				presence_state: 'arrived',
				customer: {
					name: 'John Smith',
					visit_count: 1
				},
				services: [
					{
						name: 'Shave',
						price_paise: 30000,
						duration_minutes: 15
					}
				]
			}
		]
	};

	await context.addCookies([
		{
			name: 'access_token',
			value: 'dummy.eyJyb2xlIjoiYmFyYmVyIiwiZXhwIjo5OTk5OTk5OTk5LCJsb2NhdGlvbl9pZCI6ImxvYy0xMjMiLCJuYW1lIjoiVGVzdCBTdGFmZiJ9.dummy',
			domain: 'localhost',
			path: '/'
		}
	]);

	await page.goto('/dashboard');
	await expect(page.locator('text=Jane Doe')).toBeVisible();
	await expect(page.getByText('Live', { exact: true })).toBeVisible();

	// Mutate snapshot state on the mock server
	currentSnapshot.queue_version = 2;
	currentSnapshot.entries[0].customer.name = 'Jane Doe Mutated';

	// Kill SSE connection by closing all connected clients on mock server
	expect(sseClients.length).toBeGreaterThan(0);
	for (const clientRes of sseClients) {
		clientRes.end();
	}
	sseClients = [];

	// Wait for dashboard to automatically reconnect and update client UI via snapshot reload
	await expect(page.locator('text=Jane Doe Mutated')).toBeVisible({ timeout: 10000 });
});
