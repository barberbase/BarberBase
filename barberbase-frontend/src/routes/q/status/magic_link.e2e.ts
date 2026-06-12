import { expect, test } from '@playwright/test';
import http from 'http';

let server: http.Server;
const mockPort = 9090;

const testToken =
	'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJjdXN0b21lcl9pZCI6ImN1c3QtMTIzIiwibG9jYXRpb25faWQiOiJsb2MtMTIzIiwidmlzaXRfaWQiOiJ2aXNpdC0xMjMiLCJleHAiOjk5OTk5OTk5OTl9.dummy_signature';

let mockEntry = {
	id: 'visit-123',
	token_number: 18,
	state: 'waiting',
	presence_state: 'remote',
	position_ahead: 5,
	estimated_wait_minutes: 40,
	services: [
		{ name: 'Mid Fade', duration_minutes: 25 },
		{ name: 'Beard Trim', duration_minutes: 15 }
	],
	party_size: 1,
	shop_name: 'Star Salon',
	location_name: 'Koramangala',
	queue_version: 1
};

let statusRequests = 0;
let arrivalRequests: any[] = [];
let sseClients: http.ServerResponse[] = [];

test.beforeAll(async () => {
	// Start a mock server to intercept SvelteKit server and client API requests
	server = http.createServer((req, res) => {
		res.setHeader('Access-Control-Allow-Origin', '*');
		res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
		res.setHeader('Access-Control-Allow-Headers', 'X-Session-Token, Content-Type, Cookie');
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
				Connection: 'keep-alive'
			});
			res.write(':\n\n'); // keep-alive comment
			sseClients.push(res);
			return;
		}

		if (url.includes('/v1/queue/my-status')) {
			statusRequests++;
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify(mockEntry));
			return;
		}

		if (url.includes('/v1/queue/on-the-way')) {
			mockEntry.presence_state = 'on_the_way';
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ presence_state: 'on_the_way', message: 'On the way!' }));
			return;
		}

		if (url.includes('/v1/queue/confirm-arrival')) {
			let body = '';
			req.on('data', (chunk) => {
				body += chunk;
			});
			req.on('end', () => {
				const parsed = JSON.parse(body);
				arrivalRequests.push(parsed);

				if (parsed.method === 'pin') {
					if (parsed.pin === '4729') {
						mockEntry.presence_state = 'arrived';
						res.writeHead(200, { 'Content-Type': 'application/json' });
						res.end(JSON.stringify({ presence_state: 'arrived', message: 'Welcome!' }));
					} else {
						res.writeHead(400, { 'Content-Type': 'application/json' });
						res.end(
							JSON.stringify({ code: 'WRONG_PIN', message: 'Wrong PIN', attempts_remaining: 3 })
						);
					}
				} else if (parsed.method === 'geolocation') {
					if (parsed.accuracy_metres > 150) {
						res.writeHead(422, { 'Content-Type': 'application/json' });
						res.end(JSON.stringify({ code: 'GPS_ACCURACY_LOW', message: 'GPS accuracy too low' }));
					} else {
						mockEntry.presence_state = 'arrived';
						res.writeHead(200, { 'Content-Type': 'application/json' });
						res.end(JSON.stringify({ presence_state: 'arrived', message: 'Welcome!' }));
					}
				} else {
					res.writeHead(400);
					res.end();
				}
			});
			return;
		}

		if (url.includes('/v1/queue/cancel')) {
			mockEntry.state = 'cancelled';
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ success: true }));
			return;
		}

		if (url.includes('/v1/queue/feedback')) {
			res.writeHead(201, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ success: true }));
			return;
		}

		res.writeHead(404);
		res.end();
	});

	await new Promise<void>((resolve, reject) => {
		server
			.listen(mockPort, () => {
				resolve();
			})
			.on('error', (err) => {
				reject(err);
			});
	});
});

test.afterAll(() => {
	server.close();
});

test.beforeEach(() => {
	statusRequests = 0;
	arrivalRequests = [];
	sseClients = [];
	mockEntry = {
		id: 'visit-123',
		token_number: 18,
		state: 'waiting',
		presence_state: 'remote',
		position_ahead: 5,
		estimated_wait_minutes: 40,
		services: [
			{ name: 'Mid Fade', duration_minutes: 25 },
			{ name: 'Beard Trim', duration_minutes: 15 }
		],
		party_size: 1,
		shop_name: 'Star Salon',
		location_name: 'Koramangala',
		queue_version: 1
	};
});

test('reload mid-session restores from URL parameters identically', async ({ page }) => {
	await page.goto(`/q/status?t=${testToken}`);
	console.log('PAGE URL IS:', page.url());
	console.log('PAGE BODY IS:', await page.innerHTML('body'));
	await expect(page.locator('text=Token #18')).toBeVisible();
	await expect(page.locator('text=Mid Fade')).toBeVisible();
	await expect(page.locator('text=Beard Trim')).toBeVisible();
	await expect(page.locator("text=I'm On My Way")).toBeVisible();

	// Reload the page
	await page.reload();

	// Assert state is identically restored
	await expect(page.locator('text=Token #18')).toBeVisible();
	await expect(page.locator('text=Mid Fade')).toBeVisible();
	await expect(page.locator('text=Beard Trim')).toBeVisible();
});

test('PIN success transitions the page to the arrived state', async ({ page }) => {
	mockEntry.presence_state = 'on_the_way';
	await page.goto(`/q/status?t=${testToken}`);

	await expect(page.locator('text=Verify Physical Arrival')).toBeVisible();
	const pinInput = page.locator('input[id="pin-input"]');
	const confirmBtn = page.locator('form button[type="submit"]');

	// Try wrong PIN first
	await pinInput.fill('0000');
	await confirmBtn.click();
	await expect(page.locator('text=Incorrect PIN. 3 attempts remaining.')).toBeVisible();

	// Enter correct PIN
	await pinInput.fill('4729');
	await confirmBtn.click();

	// Verify transitions to arrived state
	await expect(page.locator("text=You're Confirmed!")).toBeVisible();
	await expect(page.locator('text=Please wait inside the shop')).toBeVisible();
	expect(arrivalRequests.length).toBe(2);
	expect(arrivalRequests[1].pin).toBe('4729');
});

test('GPS accuracy too low fallback to PIN', async ({ page }) => {
	mockEntry.presence_state = 'on_the_way';
	await page.goto(`/q/status?t=${testToken}`);

	// Intercept Geolocation API
	await page.evaluate(() => {
		navigator.geolocation.getCurrentPosition = (success) => {
			success({
				coords: {
					latitude: 12.9716,
					longitude: 77.5946,
					accuracy: 200, // Accuracy too low (>150m)
					altitude: null,
					altitudeAccuracy: null,
					heading: null,
					speed: null
				},
				timestamp: Date.now()
			});
		};
	});

	await page.locator('button:has-text("Auto-Confirm using GPS")').click();
	await expect(
		page.locator('text=GPS accuracy too low. Please enter the PIN instead.')
	).toBeVisible();
});

test('SSE disconnect triggers immediate status refetch upon reconnection', async ({ page }) => {
	await page.goto(`/q/status?t=${testToken}`);
	await expect(page.locator('text=Token #18')).toBeVisible();

	// Capture starting request count
	const initialRequests = statusRequests;

	// Wait for SSE client to connect
	let connected = false;
	for (let i = 0; i < 20; i++) {
		if (sseClients.length > 0) {
			connected = true;
			break;
		}
		await page.waitForTimeout(250);
	}
	expect(connected).toBe(true);

	// Close all client SSE connections on mock server to simulate drop
	for (const client of sseClients) {
		client.end();
	}

	// Wait for reconnection and subsequent status refetch in Node.js
	let success = false;
	for (let i = 0; i < 20; i++) {
		if (statusRequests > initialRequests) {
			success = true;
			break;
		}
		await page.waitForTimeout(250);
	}
	expect(success).toBe(true);
});

test('Service Worker is never registered on the status page (Law 17)', async ({ page }) => {
	// Setup spy on serviceWorker.register
	await page.addInitScript(() => {
		(navigator as any).serviceWorker.register = () => {
			(window as any).swRegisterCalled = true;
			return Promise.resolve({} as any);
		};
	});

	await page.goto(`/q/status?t=${testToken}`);
	await expect(page.locator('text=Token #18')).toBeVisible();

	const registerCalled = await page.evaluate(() => (window as any).swRegisterCalled);
	expect(registerCalled).toBeUndefined();
});

test('missing token t parameter renders invalid link state', async ({ page }) => {
	await page.goto('/q/status');
	await expect(page.locator('text=Invalid Link')).toBeVisible();
	await expect(
		page.locator('text=This link is not valid. Please request a new one via WhatsApp.')
	).toBeVisible();
});
