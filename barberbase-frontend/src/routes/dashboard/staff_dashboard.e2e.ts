/* eslint-disable @typescript-eslint/no-explicit-any, @typescript-eslint/no-unsafe-function-type, @typescript-eslint/no-unused-vars */
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
				Connection: 'keep-alive'
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
			value:
				'dummy.eyJyb2xlIjoiYmFyYmVyIiwiZXhwIjo5OTk5OTk5OTk5LCJsb2NhdGlvbl9pZCI6ImxvYy0xMjMiLCJuYW1lIjoiVGVzdCBTdGFmZiJ9.dummy',
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
			value:
				'dummy.eyJyb2xlIjoiYmFyYmVyIiwiZXhwIjo5OTk5OTk5OTk5LCJsb2NhdGlvbl9pZCI6ImxvYy0xMjMiLCJuYW1lIjoiVGVzdCBTdGFmZiJ9.dummy',
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
			value:
				'dummy.eyJyb2xlIjoiYmFyYmVyIiwiZXhwIjo5OTk5OTk5OTk5LCJsb2NhdGlvbl9pZCI6ImxvYy0xMjMiLCJuYW1lIjoiVGVzdCBTdGFmZiJ9.dummy',
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

test('Push Permission Prompt is hidden on first session', async ({ page, context }) => {
	await context.addCookies([
		{
			name: 'access_token',
			value:
				'dummy.eyJyb2xlIjoiYmFyYmVyIiwiZXhwIjo5OTk5OTk5OTk5LCJsb2NhdGlvbl9pZCI6ImxvYy0xMjMiLCJuYW1lIjoiVGVzdCBTdGFmZiJ9.dummy',
			domain: 'localhost',
			path: '/'
		}
	]);

	await page.addInitScript(() => {
		// Reset storage
		localStorage.clear();
		sessionStorage.clear();
		// Mock Notification permission as default
		Object.defineProperty(window, 'Notification', {
			value: {
				permission: 'default',
				requestPermission: async () => 'default'
			},
			writable: true
		});
	});

	await page.goto('/dashboard');
	// Wait a bit to ensure it had time to mount/render
	await page.waitForTimeout(500);
	// Assert prompt is NOT visible
	const prompt = page.locator('#push-permission-prompt');
	await expect(prompt).not.toBeVisible();
});

test('Push Permission Prompt is shown on second session and can be denied gracefully (Law 21)', async ({
	page,
	context
}) => {
	await context.addCookies([
		{
			name: 'access_token',
			value:
				'dummy.eyJyb2xlIjoiYmFyYmVyIiwiZXhwIjo5OTk5OTk5OTk5LCJsb2NhdGlvbl9pZCI6ImxvYy0xMjMiLCJuYW1lIjoiVGVzdCBTdGFmZiJ9.dummy',
			domain: 'localhost',
			path: '/'
		}
	]);

	await page.addInitScript(() => {
		localStorage.clear();
		sessionStorage.clear();
		// Set session count to 1, so this next load makes it 2
		localStorage.setItem('bb_dash_session_count', '1');

		// Mock Notification permission as default, and requestPermission returns 'denied'
		Object.defineProperty(window, 'Notification', {
			value: {
				permission: 'default',
				requestPermission: async () => 'denied'
			},
			writable: true
		});

		// Mock serviceWorker register and ready getter using Object.defineProperty
		Object.defineProperty(navigator.serviceWorker, 'register', {
			value: async () => {
				return {
					scope: '/dashboard/',
					pushManager: {
						subscribe: async () => {
							throw new Error('Should not be called if permission is denied');
						}
					}
				} as any;
			},
			configurable: true
		});
		Object.defineProperty(navigator.serviceWorker, 'ready', {
			get: () => Promise.resolve({} as any),
			configurable: true
		});
	});

	await page.goto('/dashboard');

	// Assert prompt is visible
	const prompt = page.locator('#push-permission-prompt');
	await expect(prompt).toBeVisible();

	// Click Enable Notifications
	const enableBtn = page.locator('#btn-enable-notifications');
	await enableBtn.click();

	// Assert prompt closes silently without errors
	await expect(prompt).not.toBeVisible();
});

test('Push Permission Prompt handles network failure gracefully on subscribe POST (Law 21)', async ({
	page,
	context
}) => {
	await context.addCookies([
		{
			name: 'access_token',
			value:
				'dummy.eyJyb2xlIjoiYmFyYmVyIiwiZXhwIjo5OTk5OTk5OTk5LCJsb2NhdGlvbl9pZCI6ImxvYy0xMjMiLCJuYW1lIjoiVGVzdCBTdGFmZiJ9.dummy',
			domain: 'localhost',
			path: '/'
		}
	]);

	await page.addInitScript(() => {
		localStorage.clear();
		sessionStorage.clear();
		localStorage.setItem('bb_dash_session_count', '1');

		Object.defineProperty(window, 'Notification', {
			value: {
				permission: 'default',
				requestPermission: async () => 'granted'
			},
			writable: true
		});

		// Mock serviceWorker register, ready getter, and pushManager subscription
		Object.defineProperty(navigator.serviceWorker, 'register', {
			value: async () => {
				return {
					scope: '/dashboard/',
					pushManager: {
						subscribe: async () => {
							return {
								endpoint: 'https://fcm.googleapis.com/fcm/send/some-token',
								getKey: () => new ArrayBuffer(8)
							} as any;
						}
					}
				} as any;
			},
			configurable: true
		});
		Object.defineProperty(navigator.serviceWorker, 'ready', {
			get: () => Promise.resolve({} as any),
			configurable: true
		});

		// Mock fetch to simulate subscribe endpoint failure
		const originalFetch = window.fetch;
		window.fetch = async (input, init) => {
			const url = typeof input === 'string' ? input : input.url;
			if (url.includes('/v1/staff/push/subscribe')) {
				return Promise.reject(new Error('Network error'));
			}
			return originalFetch(input, init);
		};
	});

	await page.goto('/dashboard');

	const prompt = page.locator('#push-permission-prompt');
	await expect(prompt).toBeVisible();

	const enableBtn = page.locator('#btn-enable-notifications');
	await enableBtn.click();

	// Prompt should close and not throw/crash the page
	await expect(prompt).not.toBeVisible();
});

test('service-worker.js handles push and notificationclick event flows', async ({ page }) => {
	// 1. Go to any page (e.g. status or login) so we can evaluate JS
	await page.goto('/login');

	// 2. Evaluate script to mock SW environment and run the service worker code
	const testResults = await page.evaluate(async () => {
		// Fetch the service-worker.js code
		const response = await fetch('/service-worker.js');
		const swCode = await response.text();

		// Set up mock Service Worker scope
		const events: { [key: string]: Function[] } = {};
		const notificationsShown: any[] = [];
		const closedNotifications: any[] = [];
		let openedWindow: string | null = null;
		let focusedWindow = false;

		const mockSelf = {
			addEventListener: (event: string, callback: Function) => {
				if (!events[event]) events[event] = [];
				events[event].push(callback);
			},
			registration: {
				showNotification: async (title: string, options: any) => {
					notificationsShown.push({ title, options });
					return {
						close: () => {
							closedNotifications.push({ title, options });
						}
					};
				}
			},
			clients: {
				matchAll: async () => {
					return [
						{
							url: 'http://localhost:4173/dashboard',
							focus: async () => {
								focusedWindow = true;
								return {};
							}
						}
					];
				},
				openWindow: async (url: string) => {
					openedWindow = url;
					return {};
				}
			}
		};

		// Run SW code inside a function context with 'self' and 'clients' mocked
		const runSW = new Function('self', 'clients', 'addEventListener', 'fetch', swCode);

		// We need to pass mock fetch to intercept push-action API requests
		let lastFetchUrl: string | null = null;
		let lastFetchInit: any = null;
		let mockFetchStatus = 200;
		let mockFetchResponseJson = {};
		let mockFetchError = false;

		const mockFetch = async (url: string, init: any) => {
			lastFetchUrl = url;
			lastFetchInit = init;
			if (mockFetchError) {
				return Promise.reject(new Error('Network error'));
			}
			return {
				status: mockFetchStatus,
				ok: mockFetchStatus >= 200 && mockFetchStatus < 300,
				json: async () => mockFetchResponseJson
			} as any;
		};

		// Run the SW initialization
		runSW(mockSelf, mockSelf.clients, mockSelf.addEventListener, mockFetch);

		// Helper to invoke 'push'
		const triggerPush = async (payload: any) => {
			const pushCallback = events['push']?.[0];
			if (!pushCallback) throw new Error('No push listener registered');

			let waitPromise: Promise<any> = Promise.resolve();
			const event = {
				data: {
					json: () => payload
				},
				waitUntil: (promise: Promise<any>) => {
					waitPromise = promise;
				}
			};
			pushCallback(event);
			await waitPromise;
		};

		// Helper to invoke 'notificationclick'
		const triggerClick = async (action: string, notificationData: any) => {
			const clickCallback = events['notificationclick']?.[0];
			if (!clickCallback) throw new Error('No notificationclick listener registered');

			let waitPromise: Promise<any> = Promise.resolve();
			const notification = {
				close: () => {},
				data: notificationData
			};
			const event = {
				action,
				notification,
				waitUntil: (promise: Promise<any>) => {
					waitPromise = promise;
				}
			};
			clickCallback(event);
			await waitPromise;
		};

		const results: any = {};

		// Test Case A: Push event shows silent, scoped notification
		notificationsShown.length = 0;
		await triggerPush({
			location_name: 'Downtown',
			waiting_count: 3,
			pat: 'mock-pat',
			api_url: 'https://api.barberbase.in/v1'
		});
		results.pushEvent = {
			shownCount: notificationsShown.length,
			title: notificationsShown[0]?.title,
			body: notificationsShown[0]?.options?.body,
			silent: notificationsShown[0]?.options?.silent,
			tag: notificationsShown[0]?.options?.tag,
			requireInteraction: notificationsShown[0]?.options?.requireInteraction
		};

		// Test Case B: 200 OK on notificationclick call_next
		notificationsShown.length = 0;
		mockFetchStatus = 200;
		mockFetchResponseJson = { waiting_arrived_count: 2 };
		await triggerClick('call_next', { pat: 'mock-pat', api_url: 'https://api.barberbase.in/v1' });
		results.click200 = {
			lastFetchUrl,
			headerToken: lastFetchInit?.headers?.['X-Push-Action-Token'],
			shownCount: notificationsShown.length,
			body: notificationsShown[0]?.options?.body,
			actionsCount: notificationsShown[0]?.options?.actions?.length
		};

		// Test Case C: 401 Unauthorized updates notification to expired session
		notificationsShown.length = 0;
		mockFetchStatus = 401;
		await triggerClick('call_next', { pat: 'mock-pat', api_url: 'https://api.barberbase.in/v1' });
		results.click401 = {
			shownCount: notificationsShown.length,
			body: notificationsShown[0]?.options?.body,
			actionsCount: notificationsShown[0]?.options?.actions?.length
		};

		// Test Case D: 404 Not Found updates notification to queue clear
		notificationsShown.length = 0;
		mockFetchStatus = 404;
		await triggerClick('call_next', { pat: 'mock-pat', api_url: 'https://api.barberbase.in/v1' });
		results.click404 = {
			shownCount: notificationsShown.length,
			body: notificationsShown[0]?.options?.body,
			actionsCount: notificationsShown[0]?.options?.actions?.length
		};

		// Test Case E: 429 Too Many Requests bypasses updates (ignores)
		notificationsShown.length = 0;
		mockFetchStatus = 429;
		await triggerClick('call_next', { pat: 'mock-pat', api_url: 'https://api.barberbase.in/v1' });
		results.click429 = {
			shownCount: notificationsShown.length
		};

		// Test Case F: Network failure updates to retry
		notificationsShown.length = 0;
		mockFetchError = true;
		await triggerClick('call_next', { pat: 'mock-pat', api_url: 'https://api.barberbase.in/v1' });
		results.clickError = {
			shownCount: notificationsShown.length,
			body: notificationsShown[0]?.options?.body
		};

		return results;
	});

	// Assertions based on the returned evaluation results

	// Case A: Push
	expect(testResults.pushEvent.shownCount).toBe(1);
	expect(testResults.pushEvent.title).toContain('Downtown');
	expect(testResults.pushEvent.body).toBe('3 arrived · NEXT CLIENT ready');
	expect(testResults.pushEvent.silent).toBe(true);
	expect(testResults.pushEvent.tag).toBe('barberbase-queue');
	expect(testResults.pushEvent.requireInteraction).toBe(true);

	// Case B: 200 OK
	expect(testResults.click200.lastFetchUrl).toBe(
		'https://api.barberbase.in/v1/staff/push/call-next'
	);
	expect(testResults.click200.headerToken).toBe('mock-pat');
	expect(testResults.click200.shownCount).toBe(1);
	expect(testResults.click200.body).toContain('✓ Called · 2 remaining');

	// Case C: 401
	expect(testResults.click401.shownCount).toBe(1);
	expect(testResults.click401.body).toContain('Session expired · Open dashboard to continue');

	// Case D: 404
	expect(testResults.click404.shownCount).toBe(1);
	expect(testResults.click404.body).toContain('Queue clear · No arrived customers');

	// Case E: 429
	expect(testResults.click429.shownCount).toBe(0);

	// Case F: Network Error
	expect(testResults.clickError.shownCount).toBe(1);
	expect(testResults.clickError.body).toContain('Network error · Tap to retry');
});
