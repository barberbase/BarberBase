import { redirect, fail } from '@sveltejs/kit';
import type { PageServerLoad, Actions } from './$types';
import { ApiClient } from '$lib/api/client';

function makeClient(event: any) {
	const accessToken = event.cookies.get('access_token');
	const isTest = event.url.hostname === 'localhost' || event.url.hostname === '127.0.0.1';
	const apiBase = isTest ? 'http://127.0.0.1:9090' : undefined;
	const platformMock = apiBase ? { env: { PUBLIC_API_BASE: apiBase } } : event.platform;
	return new ApiClient(accessToken, platformMock);
}

export const load: PageServerLoad = async (event) => {
	const parentData = await event.parent();
	const locationId = parentData.staff.location_id;
	const client = makeClient(event);

	let days: any[] = [];
	try {
		const res = await client.get<{ days: any[] }>(`/v1/admin/locations/${locationId}/hours`);
		days = res.days ?? [];
	} catch (err: any) {
		if (err?.status === 401) throw redirect(302, '/login');
		if (err?.status === 403) throw redirect(302, '/dashboard');
	}

	return { days, locationId };
};

export const actions: Actions = {
	save: async (event) => {
		const client = makeClient(event);
		const form = await event.request.formData();

		const locationId = String(form.get('location_id') || '');
		const days = [];
		for (let d = 0; d < 7; d++) {
			const isOpen = form.get(`open_${d}`) === 'on';
			const opensAt = String(form.get(`opens_${d}`) || '');
			const closesAt = String(form.get(`closes_${d}`) || '');
			if (isOpen && (!opensAt || !closesAt)) {
				return fail(422, { error: `Set both opening and closing time for ${DAY_NAMES[d]}.` });
			}
			if (isOpen && opensAt >= closesAt) {
				return fail(422, { error: `${DAY_NAMES[d]}: opening time must be before closing time.` });
			}
			days.push({
				day_of_week: d,
				is_open: isOpen,
				...(isOpen ? { opens_at: opensAt, closes_at: closesAt } : {})
			});
		}

		try {
			await client.put(`/v1/admin/locations/${locationId}/hours`, { days });
			return { success: true };
		} catch (err: any) {
			if (err?.status === 401) throw redirect(302, '/login');
			return fail(err?.status || 500, {
				error: err?.data?.error || err?.data?.message || 'Failed to save business hours'
			});
		}
	}
};

const DAY_NAMES = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
