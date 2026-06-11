import { error } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async (event) => {
	const { params, url, fetch } = event;
	const tenantSlug = params.tenant_slug;
	const locationSlug = params.location_slug;
	const fullSlug = `${tenantSlug}/${locationSlug}`;

	const isTest = url.hostname === 'localhost' || url.hostname === '127.0.0.1';
	const apiBase = isTest
		? 'http://127.0.0.1:9090'
		: (event.platform?.env?.PUBLIC_API_BASE || import.meta.env.PUBLIC_API_BASE || 'http://127.0.0.1:9090');

	// 1. Get location status
	let statusRes;
	try {
		statusRes = await fetch(`${apiBase}/v1/public/locations/${encodeURIComponent(fullSlug)}/status`);
	} catch (err) {
		throw error(500, 'Network error while checking shop status');
	}

	if (statusRes.status === 404) {
		throw error(404, 'Shop not found');
	}
	if (!statusRes.ok) {
		throw error(statusRes.status, 'Failed to fetch shop status');
	}

	const location = (await statusRes.json()) as any;
	const locationId = location.id;

	// 2. Fetch service catalog & optional initial booking options in parallel
	const vParam = url.searchParams.get('v');
	const variantIds = vParam ? vParam.split(',').filter(Boolean) : [];

	const catalogPromise = fetch(`${apiBase}/v1/public/locations/${locationId}/service-catalog`).then(async (r) => {
		if (!r.ok) {
			throw error(r.status, 'Failed to fetch service catalog');
		}
		return r.json();
	});

	const optionsPromise = variantIds.length > 0
		? fetch(`${apiBase}/v1/public/locations/${locationId}/booking-options`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ variant_ids: variantIds, party_size: 1 })
			}).then(async (r) => {
				if (!r.ok) {
					return { error: 'Unable to load options, please retry' };
				}
				return r.json();
			}).catch(() => ({ error: 'Unable to load options, please retry' }))
		: Promise.resolve(null);

	let catalog;
	let initialBookingOptions;
	try {
		[catalog, initialBookingOptions] = await Promise.all([catalogPromise, optionsPromise]);
	} catch (err: any) {
		if (err.status) {
			throw err;
		}
		throw error(500, 'Failed to load catalog or options');
	}

	return {
		location,
		catalog,
		variantIds,
		initialBookingOptions,
		apiBase
	};
};
