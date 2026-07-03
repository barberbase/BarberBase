import { redirect } from '@sveltejs/kit';
import type { Cookies, Handle, HandleFetch } from '@sveltejs/kit';
import { decodeToken, isTokenExpired, getApiBase } from '$lib/api/client';

const cookieOpts = {
	httpOnly: true,
	secure: true,
	path: '/',
	sameSite: 'lax'
} as const;

function clearAuthCookies(cookies: Cookies) {
	cookies.delete('access_token', { path: '/' });
	cookies.delete('refresh_token', { path: '/' });
	cookies.delete('stream_token', { path: '/' });
}

// Refresh returns { access_token, stream_token } in the JSON body per openapi.yaml.
// (The Set-Cookie the Go API also sends is named bb_access — not usable here.)
async function captureRefreshedTokens(
	refreshRes: Response,
	cookies: Cookies
): Promise<string | null> {
	try {
		const body = (await refreshRes.json()) as { access_token?: string; stream_token?: string };
		if (body.access_token) cookies.set('access_token', body.access_token, cookieOpts);
		if (body.stream_token) cookies.set('stream_token', body.stream_token, cookieOpts);
		return body.access_token ?? null;
	} catch {
		return null;
	}
}

export const handle: Handle = async ({ event, resolve }) => {
	const url = new URL(event.request.url);
	const isProtectedRoute =
		url.pathname.startsWith('/dashboard') || url.pathname.startsWith('/admin');

	if (isProtectedRoute) {
		const accessToken = event.cookies.get('access_token');
		const refreshToken = event.cookies.get('refresh_token');

		let validToken = false;
		let claims: any = null;

		if (accessToken) {
			claims = decodeToken(accessToken);
			if (claims && !isTokenExpired(claims)) {
				validToken = true;
			}
		}

		if (!validToken) {
			if (refreshToken) {
				try {
					const apiBase = getApiBase(event.platform);
					const refreshRes = await event.fetch(`${apiBase}/v1/auth/staff/refresh`, {
						method: 'POST',
						headers: {
							Cookie: `bb_refresh=${refreshToken}`,
							'x-bff-retry': 'true'
						}
					});

					if (refreshRes.status === 200) {
						const newAccessToken = await captureRefreshedTokens(refreshRes, event.cookies);
						if (newAccessToken) {
							claims = decodeToken(newAccessToken);
							validToken = !!claims;
						}
					}
				} catch (err) {
					// Fall through to redirect
				}
			}

			if (!validToken) {
				clearAuthCookies(event.cookies);
				throw redirect(302, '/login');
			}
		}

		event.locals.staff = claims;
	}

	return resolve(event);
};

export const handleFetch: HandleFetch = async ({ event, request, fetch }) => {
	const apiBase = getApiBase(event.platform);

	if (request.url.startsWith(apiBase)) {
		let response = await fetch(request);

		if (response.status === 401) {
			if (request.headers.has('x-bff-retry')) {
				clearAuthCookies(event.cookies);
				throw redirect(302, '/login');
			}

			const refreshToken = event.cookies.get('refresh_token');
			if (!refreshToken) {
				clearAuthCookies(event.cookies);
				throw redirect(302, '/login');
			}

			const refreshRes = await fetch(`${apiBase}/v1/auth/staff/refresh`, {
				method: 'POST',
				headers: {
					Cookie: `bb_refresh=${refreshToken}`,
					'x-bff-retry': 'true'
				}
			});

			if (refreshRes.status === 200) {
				const newAccessToken = await captureRefreshedTokens(refreshRes, event.cookies);

				const newRequest = request.clone();
				if (newAccessToken) {
					newRequest.headers.set('Authorization', `Bearer ${newAccessToken}`);
				}
				newRequest.headers.set('x-bff-retry', 'true');

				response = await fetch(newRequest);

				if (response.status === 401) {
					clearAuthCookies(event.cookies);
					throw redirect(302, '/login');
				}
			} else {
				clearAuthCookies(event.cookies);
				throw redirect(302, '/login');
			}
		}

		return response;
	}

	return fetch(request);
};
