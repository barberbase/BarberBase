import { ApiClient } from '$lib/api/client';
import { redirect } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async (event) => {
	const accessToken = event.cookies.get('access_token');

	// If no token reached this load, the cookie didn't persist — bounce to login rather than 500
	if (!accessToken) {
		throw redirect(303, '/login');
	}

	const parentData = await event.parent();
	const staff = parentData.staff;
	const locationId = staff?.location_id;

	const isTest = event.url.hostname === 'localhost' || event.url.hostname === '127.0.0.1';
	const apiBase = isTest ? 'http://127.0.0.1:9090' : undefined;
	const platformMock = apiBase ? { env: { PUBLIC_API_BASE: apiBase } } : event.platform;
	const client = new ApiClient(accessToken, platformMock);

	// Fetch snapshot, staff list, and service catalog in parallel
	const [snapshot, staffMembersRes, catalog] = await Promise.all([
		client.get<any>('/v1/staff/queue/snapshot').catch((err) => {
			console.error('[PageLoad] snapshot failed:', JSON.stringify(err));
			return { entries: [], session: null };
		}),
		client.get<any>('/v1/staff/members').catch(() => ({ staff: [] })),
		client.get<any>(`/v1/public/locations/${locationId}/service-catalog`).catch(() => ({ categories: [] }))
	]);

	return {
		snapshot,
		locationId,
		accessToken,
		staffMembers: staffMembersRes?.staff || [],
		catalog,
		apiBase
	};
};
