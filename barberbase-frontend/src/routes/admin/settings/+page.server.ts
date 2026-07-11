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

export const load: PageServerLoad = async (event) => {
	const parentData = await event.parent();
	const locationId = parentData.staff.location_id;
	const client = makeClient(event);

	let settings: any = null;
	try {
		settings = await client.get<any>(`/v1/admin/locations/${locationId}/settings`);
	} catch (err: any) {
		if (err?.status === 401) throw redirect(302, '/login');
	}

	return { settings, locationId };
};

async function patchSettings(event: any, body: Record<string, any>) {
	// Actions have no parent(); decode location from the layout-verified cookie.
	const accessToken = event.cookies.get('access_token');
	if (!accessToken) throw redirect(302, '/login');
	const locationId = decodeToken(accessToken)?.location_id;
	const client = makeClient(event);
	try {
		const settings = await client.patch<any>(`/v1/admin/locations/${locationId}/settings`, body);
		return { success: true, settings };
	} catch (err: any) {
		if (err?.status === 401) throw redirect(302, '/login');
		return fail(err?.status || 500, {
			error: err?.data?.message || 'Failed to save settings'
		});
	}
}

export const actions: Actions = {
	saveRouting: async (event) => {
		const data = await event.request.formData();
		const mode = String(data.get('queue_routing_mode') || '');
		if (!['pooled', 'hybrid', 'barber_specific'].includes(mode)) {
			return fail(400, { error: 'Invalid routing mode' });
		}
		return patchSettings(event, { queue_routing_mode: mode });
	},

	saveGeofence: async (event) => {
		const data = await event.request.formData();
		const body: Record<string, any> = {
			geolocation_assist: data.get('geolocation_assist') === 'on'
		};

		const latRaw = String(data.get('gps_latitude') ?? '').trim();
		const lngRaw = String(data.get('gps_longitude') ?? '').trim();
		if ((latRaw === '') !== (lngRaw === '')) {
			return fail(400, { error: 'Latitude and longitude must be set together' });
		}
		if (latRaw !== '') {
			const lat = Number(latRaw);
			const lng = Number(lngRaw);
			if (isNaN(lat) || isNaN(lng)) return fail(400, { error: 'Coordinates must be numbers' });
			body.gps_latitude = lat;
			body.gps_longitude = lng;
		}

		const radiusRaw = String(data.get('arrival_radius_metres') ?? '').trim();
		if (radiusRaw !== '') {
			const radius = Number(radiusRaw);
			if (isNaN(radius) || radius < 20 || radius > 500) {
				return fail(400, { error: 'Radius must be between 20 and 500 metres' });
			}
			body.arrival_radius_metres = radius;
		}

		return patchSettings(event, body);
	}
};
