import { ApiClient } from '$lib/api/client';
import { error } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async (event) => {
	const token = event.url.searchParams.get('t');
	if (!token) {
		return { error: 'invalid_link', entry: null, token: null, locationId: null };
	}

	// Magic link token is NOT a JWT: two base64url segments joined by "." —
	// payload "customer_id:location_id:visit_id:unix_expires" and its HMAC.
	// The backend verifies the signature; here we only extract location_id
	// so the page can open its SSE stream.
	const parts = token.split('.');
	if (parts.length !== 2) {
		return { error: 'invalid_link', entry: null, token: null, locationId: null };
	}

	let locationId: string;
	try {
		const base64 = parts[0].replace(/-/g, '+').replace(/_/g, '/');
		const raw =
			typeof atob !== 'undefined' ? atob(base64) : Buffer.from(base64, 'base64').toString('binary');
		const fields = raw.split(':');
		locationId = fields[1];
		if (fields.length !== 4 || !locationId) {
			return { error: 'invalid_link', entry: null, token: null, locationId: null };
		}
	} catch (err) {
		console.error('DECODE ERROR:', err);
		return { error: 'invalid_link', entry: null, token: null, locationId: null };
	}

	const isTest = event.url.hostname === 'localhost' || event.url.hostname === '127.0.0.1';
	const apiBase = isTest ? 'http://127.0.0.1:9090' : undefined;
	const platformMock = apiBase ? { env: { PUBLIC_API_BASE: apiBase } } : event.platform;
	const client = new ApiClient(undefined, platformMock);

	try {
		const entry = await client.get<any>('/v1/queue/my-status', {
			headers: {
				'X-Session-Token': token
			}
		});

		return {
			entry,
			token,
			locationId,
			error: null
		};
	} catch (err: any) {
		if (err && typeof err.status === 'number') {
			if (err.status === 401) {
				return { error: 'expired', entry: null, token, locationId };
			}
			if (err.status === 404) {
				return { error: 'not_found', entry: null, token, locationId };
			}
		}
		throw error(503, 'Service Unavailable');
	}
};
