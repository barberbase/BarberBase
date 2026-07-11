import { test, expect } from '@playwright/test';
import http from 'http';

// Regression: opens_at leaked as "2000-01-01T09:00:00Z" into the closed-shop
// copy. Exact-match on the rendered string, not just "is a valid date".

let server: http.Server;
const mockPort = 9090;

test.beforeAll(async () => {
	server = http.createServer((req, res) => {
		res.setHeader('Access-Control-Allow-Origin', '*');
		const url = req.url || '';
		const json = (code: number, body: unknown) => {
			res.writeHead(code, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify(body));
		};
		if (url.includes('/status') && url.includes('/v1/public/locations/'))
			return json(200, {
				id: 'loc-123',
				name: 'Star Salon',
				slug: 'star-salon/koramangala',
				shop_status: 'closed',
				queue_open: false,
				queue_length: 0,
				estimated_wait_minutes: 0,
				business_hours_today: { opens_at: '09:00', closes_at: '21:00', is_open_today: true }
			});
		if (url.includes('/service-catalog')) return json(200, { categories: [] });
		json(200, {});
	});
	server.listen(mockPort);
});

test.afterAll(() => {
	server.close();
});

test('closed shop shows human-readable opening time', async ({ page }) => {
	await page.goto('/star-salon/koramangala');
	await expect(page.locator('.closed-card .hours-info')).toHaveText('We open today at 9:00 AM');
});
