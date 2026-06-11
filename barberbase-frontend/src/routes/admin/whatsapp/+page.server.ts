import { redirect, fail } from '@sveltejs/kit';
import type { PageServerLoad, Actions } from './$types';
import { ApiClient, decodeToken } from '$lib/api/client';

function makeClient(event: any) {
	const accessToken = event.cookies.get('access_token');
	const isTest = event.url.hostname === 'localhost' || event.url.hostname === '127.0.0.1';
	const apiBase = isTest ? 'http://127.0.0.1:9090' : undefined;
	const platformMock = apiBase ? { env: { PUBLIC_API_BASE: apiBase } } : event.platform;
	return new ApiClient(accessToken, platformMock);
}

function getLocationId(event: any): string {
	const accessToken = event.cookies.get('access_token');
	if (!accessToken) throw redirect(302, '/login');
	const claims = decodeToken(accessToken);
	if (!claims) throw redirect(302, '/login');
	return claims.location_id;
}

const REQUIRED_FIELDS = ['bhejna_config_version', 'phone_number', 'api_key', 'webhook_secret', 'whatsapp_status'];

export const load: PageServerLoad = async (event) => {
	const parentData = await event.parent();
	const locationId = parentData.staff.location_id;
	// We don't have a GET endpoint for WhatsApp mode — the mode is not directly
	// exposed to the frontend in Phase 1. We'll rely on form action results.
	return { locationId };
};

export const actions: Actions = {
	connect: async (event) => {
		const locationId = getLocationId(event);
		const client = makeClient(event);
		const data = await event.request.formData();
		const rawJson = String(data.get('config_json') || '');

		// Parse JSON client-side validation (also done server-side)
		let parsed: any;
		try {
			parsed = JSON.parse(rawJson);
		} catch {
			return fail(422, { error: 'Invalid JSON — please paste the full Bhejna config blob' });
		}

		const missing = REQUIRED_FIELDS.filter((f) => !(f in parsed));
		if (missing.length > 0) {
			return fail(422, { error: `Missing required fields: ${missing.join(', ')}` });
		}

		try {
			const res = await client.post<{ whatsapp_mode: string; webhook_url: string }>(
				`/v1/admin/locations/${locationId}/whatsapp/connect`,
				parsed
			);
			return { connected: true, webhook_url: res.webhook_url, phone_number: parsed.phone_number };
		} catch (err: any) {
			if (err?.status === 401) throw redirect(302, '/login');
			return fail(err?.status || 500, { error: err?.data?.message || 'Failed to connect WhatsApp' });
		}
	},

	disconnect: async (event) => {
		const locationId = getLocationId(event);
		const client = makeClient(event);

		try {
			await client.post(`/v1/admin/locations/${locationId}/whatsapp/disconnect`);
			return { disconnected: true };
		} catch (err: any) {
			if (err?.status === 401) throw redirect(302, '/login');
			return fail(err?.status || 500, { error: err?.data?.message || 'Failed to disconnect WhatsApp' });
		}
	}
};
