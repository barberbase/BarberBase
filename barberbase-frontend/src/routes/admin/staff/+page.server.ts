import { redirect, fail } from '@sveltejs/kit';
import type { PageServerLoad, Actions } from './$types';
import { ApiClient, decodeToken } from '$lib/api/client';
import type { components } from '$lib/api/client';

type StaffMember = components['schemas']['StaffMember'];

function makeClient(event: any) {
	const accessToken = event.cookies.get('access_token');
	const isTest = event.url.hostname === 'localhost' || event.url.hostname === '127.0.0.1';
	const apiBase = isTest ? 'http://127.0.0.1:9090' : undefined;
	const platformMock = apiBase ? { env: { PUBLIC_API_BASE: apiBase } } : event.platform;
	return new ApiClient(accessToken, platformMock);
}

function normalizePhone(raw: string): string {
	const cleaned = raw.trim();
	if (cleaned.startsWith('+')) return cleaned;
	if (cleaned.startsWith('91') && cleaned.length === 12) return '+' + cleaned;
	if (cleaned.length === 10) return '+91' + cleaned;
	return cleaned;
}

export const load: PageServerLoad = async (event) => {
	const client = makeClient(event);

	let staffMembers: StaffMember[] = [];
	try {
		const res = await client.get<{ staff?: StaffMember[] }>('/v1/staff/members');
		staffMembers = res.staff || [];
	} catch (err: any) {
		if (err?.status === 401) throw redirect(302, '/login');
	}

	return { staffMembers };
};

export const actions: Actions = {
	addMember: async (event) => {
		const client = makeClient(event);
		const data = await event.request.formData();
		const name = String(data.get('name') || '').trim();
		const rawPhone = String(data.get('phone_number') || '').trim();
		const role = String(data.get('role') || 'barber');

		if (!name || !rawPhone) {
			return fail(422, { error: 'Name and phone number are required' });
		}
		if (role !== 'manager' && role !== 'barber') {
			return fail(422, { error: 'Role must be manager or barber' });
		}

		const phone_number = normalizePhone(rawPhone);

		try {
			await client.post('/v1/admin/staff', { name, phone_number, role });
			return { success: true };
		} catch (err: any) {
			if (err?.status === 401) throw redirect(302, '/login');
			return fail(err?.status || 500, {
				error: err?.data?.message || 'Failed to add staff member'
			});
		}
	}
};
