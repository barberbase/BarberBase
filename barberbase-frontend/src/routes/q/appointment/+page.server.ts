import { ApiClient } from '$lib/api/client';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async (event) => {
	const token = event.url.searchParams.get('t');
	if (!token) {
		return { error: 'invalid_link', apt: null, token: null };
	}

	const isTest = event.url.hostname === 'localhost' || event.url.hostname === '127.0.0.1';
	const apiBase = isTest ? 'http://127.0.0.1:9090' : undefined;
	const platformMock = apiBase ? { env: { PUBLIC_API_BASE: apiBase } } : event.platform;
	const client = new ApiClient(undefined, platformMock);

	try {
		const apt = await client.get<any>('/v1/appointments/my', {
			headers: { 'X-Session-Token': token }
		});
		return { apt, token, error: null };
	} catch (err: any) {
		if (err && typeof err.status === 'number') {
			if (err.status === 401) return { error: 'expired', apt: null, token };
			if (err.status === 404) return { error: 'not_found', apt: null, token };
		}
		return { error: 'unavailable', apt: null, token };
	}
};
