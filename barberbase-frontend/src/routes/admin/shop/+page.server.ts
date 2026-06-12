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

export const load: PageServerLoad = async (event) => {
	const parentData = await event.parent();
	const locationId = parentData.staff.location_id;
	const client = makeClient(event);

	let shopStatus: any = null;
	try {
		shopStatus = await client.get<any>('/v1/staff/shop/status');
	} catch (err: any) {
		if (err?.status === 401) throw redirect(302, '/login');
	}

	return { shopStatus, locationId };
};

export const actions: Actions = {
	setStatus: async (event) => {
		const locationId = getLocationId(event);
		const client = makeClient(event);
		const data = await event.request.formData();

		const status = String(data.get('status') || '');
		const expires_in_minutes_raw = data.get('expires_in_minutes');
		const modal_action = data.get('modal_action') ? String(data.get('modal_action')) : null;

		const body: Record<string, any> = { status };
		if (expires_in_minutes_raw !== null && expires_in_minutes_raw !== '') {
			const val = Number(expires_in_minutes_raw);
			body.expires_in_minutes = isNaN(val) ? null : val;
		}
		if (modal_action) {
			body.modal_action = modal_action;
		}

		try {
			await client.patch('/v1/staff/shop/status', body);
			return { success: true };
		} catch (err: any) {
			if (err?.status === 401) throw redirect(302, '/login');
			if (err?.status === 422) {
				return fail(422, {
					needs_modal: true,
					active_entry_count: err?.data?.active_entry_count ?? 0,
					pending_status: status,
					pending_expires: expires_in_minutes_raw !== null ? Number(expires_in_minutes_raw) : null
				});
			}
			return fail(err?.status || 500, {
				error: err?.data?.message || 'Failed to update shop status'
			});
		}
	},

	regeneratePin: async (event) => {
		const locationId = getLocationId(event);
		const client = makeClient(event);

		try {
			const res = await client.post<{ new_pin?: string }>(
				`/v1/admin/locations/${locationId}/arrival-pin/regenerate`
			);
			return { new_pin: res.new_pin, pin_success: true };
		} catch (err: any) {
			if (err?.status === 401) throw redirect(302, '/login');
			return fail(err?.status || 500, { error: err?.data?.message || 'Failed to regenerate PIN' });
		}
	}
};
