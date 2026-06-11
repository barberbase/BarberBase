import { expect, test } from '@playwright/test';
import http from 'http';

let server: http.Server;
const mockPort = 9090;

test.beforeAll(() => {
	// Start a mock server to intercept SvelteKit server-to-server requests
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
		const authHeader = req.headers['authorization'];

		if (url.includes('/v1/auth/staff/refresh')) {
			const cookieHeader = req.headers['cookie'] || '';
			const hasRefreshToken = cookieHeader.includes('refresh_token=valid_refresh');
			if (hasRefreshToken) {
				res.writeHead(200, {
					'Content-Type': 'application/json',
					'Set-Cookie': 'access_token=new_valid_access_token; Path=/; HttpOnly; Secure'
				});
				res.end(JSON.stringify({ success: true }));
			} else {
				res.writeHead(401);
				res.end(JSON.stringify({ error: 'Invalid refresh token' }));
			}
			return;
		}

		if (url.includes('/v1/staff/queue/snapshot')) {
			if (authHeader === 'Bearer new_valid_access_token') {
				res.writeHead(200, { 'Content-Type': 'application/json' });
				res.end(JSON.stringify({ entries: [] }));
			} else {
				res.writeHead(401, { 'Content-Type': 'application/json' });
				res.end(JSON.stringify({ error: 'Unauthorized' }));
			}
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

test('should refresh expired access_token and load dashboard successfully', async ({
	page,
	context
}) => {
	// 1. Set expired access_token (exp in past) and a valid refresh_token
	await context.addCookies([
		{
			name: 'access_token',
			value: 'dummy.eyJyb2xlIjoiYmFyYmVyIiwiZXhwIjoxMDAwfQ.dummy',
			domain: 'localhost',
			path: '/'
		},
		{
			name: 'refresh_token',
			value: 'valid_refresh',
			domain: 'localhost',
			path: '/'
		}
	]);

	// 2. Navigate to /dashboard
	await page.goto('/dashboard');

	// 3. Confirm we did not redirect to /login
	await expect(page.url()).toContain('/dashboard');

	// 4. Verify access_token cookie is updated to the new token value
	const cookies = await context.cookies();
	const accessTokenCookie = cookies.find((c) => c.name === 'access_token');
	expect(accessTokenCookie).toBeDefined();
	expect(accessTokenCookie?.value).toBe('new_valid_access_token');
});
