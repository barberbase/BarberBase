import { test } from '@playwright/test';
import http from 'http';

// Screenshot-only spec for mobile-first verification at 360px.
// Skipped in normal suite runs; enable with SCREENSHOTS=1.
// Output: test-results/snapshots/*.png
test.skip(!process.env.SCREENSHOTS, 'screenshot pass only');

let server: http.Server;
const mockPort = 9090;

const magicToken = 'Y3VzdC0xMjM6bG9jLTEyMzp2aXNpdC0xMjM6OTk5OTk5OTk5OQ.dummy_signature';

// Dummy staff JWT: header.payload.sig with role=owner (hooks only decodes, no verify server-side here)
const ownerJwt =
	'dummy.' +
	Buffer.from(
		JSON.stringify({
			role: 'owner',
			exp: 9999999999,
			location_id: 'loc-123',
			tenant_id: 't-1',
			staff_member_id: 's-1',
			name: 'Owner'
		})
	)
		.toString('base64')
		.replace(/=+$/, '') +
	'.dummy';

const entry: Record<string, unknown> = {
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

// 14 variants across 3 groups → guaranteed overflow at 360×780
const bigCatalog = {
	location_id: 'loc-123',
	display_mode: 'hierarchical',
	categories: [
		{
			id: 'c1',
			name: 'Hair',
			gender: 'men',
			groups: [
				{
					id: 'g1',
					name: 'Fades',
					variants: Array.from({ length: 6 }, (_, i) => ({
						id: `v-fade-${i}`,
						name: ['Low Fade', 'Mid Fade', 'High Fade', 'Skin Fade', 'Drop Fade', 'Burst Fade'][i],
						duration_minutes: 25,
						price_paise: 20000 + i * 5000,
						allow_walk_in: true,
						allow_appointment: true,
						requires_appointment: false,
						is_popular: i === 1
					}))
				},
				{
					id: 'g2',
					name: 'Classic Cuts',
					variants: Array.from({ length: 4 }, (_, i) => ({
						id: `v-cut-${i}`,
						name: ['Buzz Cut', 'Crew Cut', 'Scissor Cut', 'Kids Cut'][i],
						duration_minutes: 20,
						price_paise: 15000,
						allow_walk_in: true,
						allow_appointment: true,
						requires_appointment: false,
						is_popular: false
					}))
				},
				{
					id: 'g3',
					name: 'Beard & Shave',
					variants: Array.from({ length: 4 }, (_, i) => ({
						id: `v-beard-${i}`,
						name: ['Beard Trim', 'Hot Towel Shave', 'Beard Sculpt', 'Head Shave'][i],
						duration_minutes: 15,
						price_paise: 10000,
						allow_walk_in: true,
						allow_appointment: true,
						requires_appointment: false,
						is_popular: false
					}))
				}
			]
		}
	]
};

const hoursDays = Array.from({ length: 7 }, (_, d) => ({
	day_of_week: d,
	is_open: d !== 0,
	...(d !== 0 ? { opens_at: '09:00', closes_at: '21:00' } : {})
}));

test.beforeAll(async () => {
	server = http.createServer((req, res) => {
		res.setHeader('Access-Control-Allow-Origin', '*');
		res.setHeader('Access-Control-Allow-Methods', 'GET, POST, PATCH, PUT, OPTIONS');
		res.setHeader('Access-Control-Allow-Headers', 'X-Session-Token, Content-Type, Authorization');
		if (req.method === 'OPTIONS') {
			res.writeHead(200);
			res.end();
			return;
		}
		const url = req.url || '';
		const json = (code: number, body: unknown) => {
			res.writeHead(code, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify(body));
		};

		if (url.includes('/v1/stream/')) {
			res.writeHead(200, { 'Content-Type': 'text/event-stream' });
			res.write(':\n\n');
			return;
		}
		if (url.includes('/v1/queue/my-status')) return json(200, entry);
		if (url.includes('/status') && url.includes('/v1/public/locations/'))
			return json(200, {
				id: 'loc-123',
				name: 'Star Salon',
				slug: 'star-salon/koramangala',
				shop_status: 'open',
				queue_open: true,
				queue_length: 4,
				estimated_wait_minutes: 35,
				business_hours_today: { opens_at: '09:00', closes_at: '21:00', is_open_today: true }
			});
		if (url.includes('/service-catalog')) return json(200, bigCatalog);
		if (url.includes('/booking-options'))
			return json(200, {
				total_duration_minutes: 25,
				total_price_paise: 25000,
				allowed_modes: ['walk_in'],
				queue_length: 4,
				estimated_wait_minutes: 35
			});
		if (url.includes('/hours')) return json(200, { days: hoursDays });
		if (url.includes('/v1/admin/locations/') && url.includes('/services'))
			return json(200, bigCatalog);
		if (url.includes('/v1/staff/shop/status'))
			return json(200, {
				shop_status: 'open',
				queue_session_status: 'active',
				tenant_slug: 'star-salon',
				location_slug: 'star-salon/koramangala',
				arrival_pin: '476212',
				manual_override_active: false
			});
		if (url.includes('/v1/staff/queue/snapshot'))
			return json(200, { queue_version: 1, session_status: 'active', entries: [] });
		if (url.includes('/v1/staff/members'))
			return json(200, {
				staff: [
					{ id: 's-1', name: 'Arjun', role: 'barber', status: 'cutting' },
					{ id: 's-2', name: 'Vikram', role: 'barber', status: 'idle' },
					{ id: 's-3', name: 'Sameer', role: 'barber', status: 'break' }
				]
			});
		if (url.includes('/v1/staff/analytics/daily'))
			return json(200, {
				business_date: '2026-07-04',
				total_visits: 12,
				total_revenue_paise: 184500,
				average_wait_minutes: 18,
				no_show_count: 1,
				cancelled_count: 0,
				barber_breakdown: []
			});
		json(200, {});
	});
	server.listen(mockPort);
});

test.afterAll(() => {
	server.close();
});

test.use({ viewport: { width: 360, height: 780 } });

const shot = (name: string) => `test-results/snapshots/${name}.png`;

test('q/status remote state — cues', async ({ page }) => {
	entry.state = 'waiting';
	entry.presence_state = 'remote';
	await page.goto(`/q/status?t=${magicToken}`);
	await page.waitForSelector('text=I\'m On My Way');
	await page.screenshot({ path: shot('q-status-remote-360'), fullPage: true });
});

test('q/status on_the_way — PIN cue', async ({ page }) => {
	entry.state = 'waiting';
	entry.presence_state = 'on_the_way';
	await page.goto(`/q/status?t=${magicToken}`);
	await page.waitForSelector('text=Confirm you've arrived');
	await page.screenshot({ path: shot('q-status-ontheway-360'), fullPage: true });
});

test('q/status called — urgency copy', async ({ page }) => {
	entry.state = 'called';
	entry.presence_state = 'arrived';
	await page.goto(`/q/status?t=${magicToken}`);
	await page.waitForSelector("text=It's Your Turn!");
	await page.screenshot({ path: shot('q-status-called-360'), fullPage: true });
});

test('shop page — scroll affordance pill', async ({ page }) => {
	await page.goto('/star-salon/koramangala');
	await page.waitForSelector('text=Select Services');
	await page.waitForTimeout(600); // allow $effect count
	await page.screenshot({ path: shot('shop-affordance-top-360') });
	// scroll to end: pill must disappear
	await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
	await page.waitForTimeout(400);
	await page.screenshot({ path: shot('shop-affordance-bottom-360') });
});

test('admin hours — shadcn rebuild', async ({ page, context }) => {
	await context.addCookies([
		{ name: 'access_token', value: ownerJwt, domain: 'localhost', path: '/' }
	]);
	await page.goto('/admin/hours');
	await page.waitForSelector('text=Weekly schedule');
	await page.screenshot({ path: shot('admin-hours-shadcn-360'), fullPage: true });
});

test('admin hours — desktop', async ({ page, context }) => {
	await page.setViewportSize({ width: 1280, height: 800 });
	await context.addCookies([
		{ name: 'access_token', value: ownerJwt, domain: 'localhost', path: '/' }
	]);
	await page.goto('/admin/hours');
	await page.waitForSelector('text=Weekly schedule');
	await page.screenshot({ path: shot('admin-hours-shadcn-1280'), fullPage: true });
});

test('dashboard — role-gated admin link', async ({ page, context }) => {
	await context.addCookies([
		{ name: 'access_token', value: ownerJwt, domain: 'localhost', path: '/' }
	]);
	await page.goto('/dashboard');
	await page.waitForSelector('text=Staff Dashboard');
	await page.screenshot({ path: shot('dashboard-admin-link-360') });
});

test('dashboard — today-at-a-glance strip', async ({ page, context }) => {
	await context.addCookies([
		{ name: 'access_token', value: ownerJwt, domain: 'localhost', path: '/' }
	]);
	await page.goto('/dashboard');
	await page.waitForSelector('text=Revenue Today');
	await page.screenshot({ path: shot('dashboard-glance-360'), fullPage: true });
	await page.setViewportSize({ width: 1280, height: 800 });
	await page.waitForTimeout(200);
	await page.screenshot({ path: shot('dashboard-glance-1280') });
});
