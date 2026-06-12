import { redirect, fail } from '@sveltejs/kit';
import type { PageServerLoad, Actions } from './$types';
import { ApiClient, decodeToken } from '$lib/api/client';
import type { components } from '$lib/api/client';

type ServiceCatalog = components['schemas']['ServiceCatalog'];

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

	let catalog: ServiceCatalog = {
		location_id: locationId,
		display_mode: 'hierarchical',
		categories: []
	};
	try {
		catalog = await client.get<ServiceCatalog>(`/v1/admin/locations/${locationId}/services`);
	} catch (err: any) {
		if (err?.status === 401) throw redirect(302, '/login');
	}

	return { catalog, locationId };
};

export const actions: Actions = {
	createVariant: async (event) => {
		const locationId = getLocationId(event);
		const client = makeClient(event);
		const data = await event.request.formData();

		const priceRupees = Number(data.get('price_rupees'));
		if (!Number.isInteger(priceRupees) || priceRupees < 0) {
			return fail(422, { error: 'Price must be a non-negative whole number in rupees' });
		}
		const price_paise = priceRupees * 100;

		try {
			await client.post(`/v1/admin/locations/${locationId}/services`, {
				category_name: String(data.get('category_name') || ''),
				category_gender: String(data.get('category_gender') || 'unisex'),
				group_name: String(data.get('group_name') || ''),
				variant_name: String(data.get('variant_name') || ''),
				duration_minutes: Number(data.get('duration_minutes')),
				price_paise,
				allow_walk_in: data.get('allow_walk_in') !== 'false',
				allow_appointment: data.get('allow_appointment') !== 'false',
				requires_appointment: data.get('requires_appointment') === 'true',
				is_popular: data.get('is_popular') === 'true'
			});
			return { success: true };
		} catch (err: any) {
			if (err?.status === 401) throw redirect(302, '/login');
			return fail(err?.status || 500, { error: err?.data?.message || 'Failed to create service' });
		}
	},

	updateVariant: async (event) => {
		const locationId = getLocationId(event);
		const client = makeClient(event);
		const data = await event.request.formData();
		const variantId = String(data.get('variant_id') || '');

		const body: Record<string, any> = {};
		if (data.has('variant_name')) body.variant_name = String(data.get('variant_name'));
		if (data.has('duration_minutes')) body.duration_minutes = Number(data.get('duration_minutes'));
		if (data.has('price_rupees')) {
			const priceRupees = Number(data.get('price_rupees'));
			if (!Number.isInteger(priceRupees) || priceRupees < 0) {
				return fail(422, { error: 'Price must be a non-negative whole number in rupees' });
			}
			body.price_paise = priceRupees * 100;
		}
		if (data.has('is_active')) body.is_active = data.get('is_active') !== 'false';
		if (data.has('is_popular')) body.is_popular = data.get('is_popular') === 'true';

		try {
			await client.patch(`/v1/admin/locations/${locationId}/services/${variantId}`, body);
			return { success: true };
		} catch (err: any) {
			if (err?.status === 401) throw redirect(302, '/login');
			return fail(err?.status || 500, { error: err?.data?.message || 'Failed to update service' });
		}
	},

	deactivateVariant: async (event) => {
		const locationId = getLocationId(event);
		const client = makeClient(event);
		const data = await event.request.formData();
		const variantId = String(data.get('variant_id') || '');

		try {
			await client.patch(`/v1/admin/locations/${locationId}/services/${variantId}`, {
				is_active: false
			});
			return { success: true };
		} catch (err: any) {
			if (err?.status === 401) throw redirect(302, '/login');
			return fail(err?.status || 500, {
				error: err?.data?.message || 'Failed to deactivate service'
			});
		}
	}
};
