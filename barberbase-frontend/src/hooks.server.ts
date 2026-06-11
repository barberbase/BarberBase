import { redirect } from '@sveltejs/kit';
import type { Handle, HandleFetch } from '@sveltejs/kit';
import { decodeToken, isTokenExpired, getApiBase } from '$lib/api/client';

function parseCookie(cookieString: string, name: string): string | null {
	const matches = cookieString.match(new RegExp(`(^|;)\\s*${name}\\s*=\\s*([^;]+)`));
	return matches ? matches[2].trim() : null;
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
							Cookie: `refresh_token=${refreshToken}`,
							'x-bff-retry': 'true'
						}
					});

					if (refreshRes.status === 200) {
						const setCookieHeaders = refreshRes.headers.getSetCookie();
						let newAccessToken: string | null = null;
						let newRefreshToken: string | null = null;

						for (const cookieStr of setCookieHeaders) {
							const acc = parseCookie(cookieStr, 'access_token');
							if (acc) newAccessToken = acc;
							const ref = parseCookie(cookieStr, 'refresh_token');
							if (ref) newRefreshToken = ref;
						}

						if (newAccessToken) {
							event.cookies.set('access_token', newAccessToken, {
								httpOnly: true,
								secure: true,
								path: '/',
								sameSite: 'lax'
							});
							claims = decodeToken(newAccessToken);
							validToken = !!claims;
						}
						if (newRefreshToken) {
							event.cookies.set('refresh_token', newRefreshToken, {
								httpOnly: true,
								secure: true,
								path: '/',
								sameSite: 'lax'
							});
						}
					}
				} catch (err) {
					// Fall through to redirect
				}
			}

			if (!validToken) {
				event.cookies.delete('access_token', { path: '/' });
				event.cookies.delete('refresh_token', { path: '/' });
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
				event.cookies.delete('access_token', { path: '/' });
				event.cookies.delete('refresh_token', { path: '/' });
				throw redirect(302, '/login');
			}

			const refreshToken = event.cookies.get('refresh_token');
			if (!refreshToken) {
				event.cookies.delete('access_token', { path: '/' });
				event.cookies.delete('refresh_token', { path: '/' });
				throw redirect(302, '/login');
			}

			const refreshRes = await fetch(`${apiBase}/v1/auth/staff/refresh`, {
				method: 'POST',
				headers: {
					Cookie: `refresh_token=${refreshToken}`,
					'x-bff-retry': 'true'
				}
			});

			if (refreshRes.status === 200) {
				const setCookieHeaders = refreshRes.headers.getSetCookie();
				let newAccessToken: string | null = null;
				let newRefreshToken: string | null = null;

				for (const cookieStr of setCookieHeaders) {
					const acc = parseCookie(cookieStr, 'access_token');
					if (acc) newAccessToken = acc;
					const ref = parseCookie(cookieStr, 'refresh_token');
					if (ref) newRefreshToken = ref;
				}

				if (newAccessToken) {
					event.cookies.set('access_token', newAccessToken, {
						httpOnly: true,
						secure: true,
						path: '/',
						sameSite: 'lax'
					});
				}
				if (newRefreshToken) {
					event.cookies.set('refresh_token', newRefreshToken, {
						httpOnly: true,
						secure: true,
						path: '/',
						sameSite: 'lax'
					});
				}

				const newRequest = request.clone();
				if (newAccessToken) {
					newRequest.headers.set('Authorization', `Bearer ${newAccessToken}`);
				}
				newRequest.headers.set('x-bff-retry', 'true');

				response = await fetch(newRequest);

				if (response.status === 401) {
					event.cookies.delete('access_token', { path: '/' });
					event.cookies.delete('refresh_token', { path: '/' });
					throw redirect(302, '/login');
				}
			} else {
				event.cookies.delete('access_token', { path: '/' });
				event.cookies.delete('refresh_token', { path: '/' });
				throw redirect(302, '/login');
			}
		}

		return response;
	}

	return fetch(request);
};
