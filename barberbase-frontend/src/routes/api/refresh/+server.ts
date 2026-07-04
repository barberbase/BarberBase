import { json } from '@sveltejs/kit';
import { getApiBase } from '$lib/api/client';
import type { RequestHandler } from './$types';

const cookieOpts = {
	httpOnly: true,
	secure: true,
	path: '/',
	sameSite: 'lax'
} as const;

// Browser-side ApiClient calls this on 401: the httpOnly refresh_token cookie
// never leaves this origin, so the refresh against the Go API must happen here.
export const POST: RequestHandler = async ({ cookies, fetch, platform }) => {
	const refreshToken = cookies.get('refresh_token');
	if (!refreshToken) {
		return json({ error: 'no_refresh_token' }, { status: 401 });
	}

	const apiBase = getApiBase(platform);
	const res = await fetch(`${apiBase}/v1/auth/staff/refresh`, {
		method: 'POST',
		headers: { Cookie: `bb_refresh=${refreshToken}`, 'x-bff-retry': 'true' }
	});

	if (res.status !== 200) {
		cookies.delete('access_token', { path: '/' });
		cookies.delete('refresh_token', { path: '/' });
		cookies.delete('stream_token', { path: '/' });
		return json({ error: 'refresh_failed' }, { status: 401 });
	}

	const body = (await res.json()) as { access_token?: string; stream_token?: string };
	if (!body.access_token) {
		return json({ error: 'refresh_failed' }, { status: 401 });
	}

	cookies.set('access_token', body.access_token, cookieOpts);
	if (body.stream_token) cookies.set('stream_token', body.stream_token, cookieOpts);

	return json({ access_token: body.access_token });
};
