import { ApiClient } from '$lib/api/client';
import { error } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async (event) => {
	const token = event.url.searchParams.get('t');
	if (!token) {
		return { error: 'invalid_link', entry: null, token: null, locationId: null };
	}

	const parts = token.split('.');
	if (parts.length !== 3) {
		return { error: 'invalid_link', entry: null, token: null, locationId: null };
	}

	let locationId: string;
	try {
		const base64 = parts[1].replace(/-/g, '+').replace(/_/g, '/');
		const raw =
			typeof atob !== 'undefined' ? atob(base64) : Buffer.from(base64, 'base64').toString('binary');
		const utf8 = decodeURIComponent(
			raw
				.split('')
				.map((c) => '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2))
				.join('')
		);
		const payload = JSON.parse(utf8);
		locationId = payload.location_id;
		if (!locationId) {
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
