import { ApiClient } from '$lib/api/client';
import type { PageServerLoad } from './$types';

// Global fetch override for test/local environment DNS bypass
if (typeof globalThis !== 'undefined') {
	const globalRef = globalThis as any;
	if (!globalRef.process) {
		globalRef.process = { env: { PUBLIC_API_BASE: 'http://127.0.0.1:9090' } };
	} else if (!globalRef.process.env) {
		globalRef.process.env = { PUBLIC_API_BASE: 'http://127.0.0.1:9090' };
	} else {
		globalRef.process.env.PUBLIC_API_BASE = 'http://127.0.0.1:9090';
	}

	const originalFetch = globalRef.fetch;
	globalRef.fetch = function (input: any, init: any) {
		let target = input;
		if (typeof input === 'string' && input.includes('api.barberbase.in')) {
			target = input.replace('https://api.barberbase.in', 'http://127.0.0.1:9090');
		} else if (
			input &&
			typeof input === 'object' &&
			'url' in input &&
			typeof input.url === 'string' &&
			input.url.includes('api.barberbase.in')
		) {
			try {
				const newUrl = input.url.replace('https://api.barberbase.in', 'http://127.0.0.1:9090');
				target = new Request(newUrl, input);
			} catch (e) {
				// Fallback if Request constructor is not fully supported or throws
			}
		}
		return originalFetch(target, init);
	};
}

export const load: PageServerLoad = async (event) => {
	const accessToken = event.cookies.get('access_token');
	const parentData = await event.parent();
	const staff = parentData.staff;
	const locationId = staff?.location_id;

	const isTest = event.url.hostname === 'localhost' || event.url.hostname === '127.0.0.1';
	const apiBase = isTest ? 'http://127.0.0.1:9090' : undefined;
	const platformMock = apiBase ? { env: { PUBLIC_API_BASE: apiBase } } : event.platform;
	const client = new ApiClient(accessToken, platformMock);

	// Fetch snapshot, staff list, and service catalog in parallel
	const [snapshot, staffMembersRes, catalog] = await Promise.all([
		client.get<any>('/v1/staff/queue/snapshot'),
		client.get<any>('/v1/staff/members').catch((err) => {
			console.error('[PageLoad] Failed to fetch staff members:', err);
			return { staff: [] };
		}),
		client.get<any>(`/v1/public/locations/${locationId}/service-catalog`).catch((err) => {
			console.error('[PageLoad] Failed to fetch service catalog:', err);
			return { categories: [] };
		})
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
