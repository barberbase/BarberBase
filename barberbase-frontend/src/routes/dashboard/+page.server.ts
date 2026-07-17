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

	// Fetch snapshot, staff list, service catalog, and today's analytics in parallel
	const [snapshot, staffMembersRes, catalog, dailyAnalytics, staffShopStatus, appointments] = await Promise.all([
		client.get<any>('/v1/staff/queue/snapshot').catch((err) => {
			console.error('[PageLoad] snapshot failed:', JSON.stringify(err));
			return { entries: [], session: null };
		}),
		client.get<any>('/v1/staff/members').catch(() => ({ staff: [] })),
		client.get<any>(`/v1/public/locations/${locationId}/service-catalog`).catch(() => ({ categories: [] })),
		// ponytail: loaded once per page load; add SSE-triggered refetch if stale numbers bother anyone
		client.get<any>('/v1/staff/analytics/daily').catch(() => null),
		client.get<any>('/v1/staff/shop/status').catch(() => null),
		client.get<any>('/v1/staff/appointments').catch(() => null)
	]);

	// Shop open/closed + today's hours for the header. The staff endpoint has no
	// hours and the admin hours endpoint is owner/manager-only, so chain through
	// the public status endpoint (needs the slugs from the staff response).
	let shopToday = null;
	if (staffShopStatus?.tenant_slug && staffShopStatus?.location_slug) {
		const slug = encodeURIComponent(`${staffShopStatus.tenant_slug}/${staffShopStatus.location_slug}`);
		shopToday = await client.get<any>(`/v1/public/locations/${slug}/status`).catch(() => null);
	}

	// SSE connects with the 12h stream token, not the 15-min access token.
	// Fallback to accessToken covers sessions that logged in before stream_token existed.
	const streamToken = event.cookies.get('stream_token') ?? accessToken;

	return {
		snapshot,
		locationId,
		accessToken,
		streamToken,
		staffMembers: staffMembersRes?.staff || [],
		catalog,
		dailyAnalytics,
		shopToday,
		appointments,
		apiBase
	};
};
