import { redirect } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';
import { ApiClient } from '$lib/api/client';
import type { components } from '$lib/api/client';

type ServiceCatalog = components['schemas']['ServiceCatalog'];

function getApiBase(event: any): string {
	const isTest =
		event.url.hostname === 'localhost' || event.url.hostname === '127.0.0.1';
	return isTest ? 'http://127.0.0.1:9090' : undefined as any;
}

function makeClient(event: any) {
	const accessToken = event.cookies.get('access_token');
	const apiBase = getApiBase(event);
	const platformMock = apiBase ? { env: { PUBLIC_API_BASE: apiBase } } : event.platform;
	return new ApiClient(accessToken, platformMock);
}

export const load: PageServerLoad = async (event) => {
	const parentData = await event.parent();
	const staff = parentData.staff;
	const locationId = staff.location_id;

	const client = makeClient(event);

	let catalog: ServiceCatalog = { location_id: locationId, display_mode: 'hierarchical', categories: [] };
	try {
		catalog = await client.get<ServiceCatalog>(`/v1/admin/locations/${locationId}/services`);
	} catch (err: any) {
		if (err?.status === 401) throw redirect(302, '/login');
		// Empty catalog on error — will trigger wizard
	}

	const isFirstTime = !catalog.categories || catalog.categories.length === 0;

	return {
		catalog,
		locationId,
		isFirstTime
	};
};
