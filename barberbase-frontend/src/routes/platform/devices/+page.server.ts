import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { getApiBase } from '$lib/api/client';

export interface DeviceButton {
	id: string;
	button_code: string;
	label: string;
	staff_member_id: string | null;
}

export interface Device {
	id: string;
	label: string;
	is_active: boolean;
	last_seen_at: string | null;
	buttons: DeviceButton[];
}

export interface StaffMember {
	id: string;
	name: string;
	role: string;
}

// ponytail: no separate "fetchDevices" helper — one load(), one small POST/PATCH per action, nothing to share yet.

export const load: PageServerLoad = async ({ url, platform, fetch }) => {
	const location_id = url.searchParams.get('location_id') || '';
	const tenant_id = url.searchParams.get('tenant_id') || '';

	if (!location_id) {
		return { location_id, tenant_id, devices: [] as Device[], staff: [] as StaffMember[] };
	}

	const key = platform?.env?.PLATFORM_ADMIN_KEY;
	if (!key) {
		return {
			location_id,
			tenant_id,
			devices: [] as Device[],
			staff: [] as StaffMember[],
			loadError: 'Console not configured (admin key missing).'
		};
	}

	const apiBase = getApiBase(platform);
	try {
		const res = await fetch(
			`${apiBase}/v1/admin/devices?location_id=${encodeURIComponent(location_id)}`,
			{ headers: { 'X-Platform-Admin-Key': key } }
		);
		if (!res.ok) {
			return {
				location_id,
				tenant_id,
				devices: [] as Device[],
				staff: [] as StaffMember[],
				loadError: `Failed to load devices (HTTP ${res.status}).`
			};
		}
		const body = (await res.json()) as { devices: Device[]; staff: StaffMember[] };
		return {
			location_id,
			tenant_id,
			devices: body.devices ?? [],
			staff: body.staff ?? []
		};
	} catch {
		return {
			location_id,
			tenant_id,
			devices: [] as Device[],
			staff: [] as StaffMember[],
			loadError: 'Failed to reach the API.'
		};
	}
};

export const actions: Actions = {
	createDevice: async (event) => {
		const data = await event.request.formData();
		const label = (data.get('label') as string)?.trim() || '';
		const location_id = event.url.searchParams.get('location_id') || '';
		const tenant_id = event.url.searchParams.get('tenant_id') || '';

		if (!location_id || !tenant_id) {
			return fail(400, { error: 'Location ID and Tenant ID are required.' });
		}
		if (!label) {
			return fail(400, { error: 'Device label is required.' });
		}

		const key = event.platform?.env?.PLATFORM_ADMIN_KEY;
		if (!key) {
			return fail(500, { error: 'Console not configured (admin key missing).' });
		}

		const apiBase = getApiBase(event.platform);
		try {
			const res = await event.fetch(`${apiBase}/v1/admin/devices`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', 'X-Platform-Admin-Key': key },
				body: JSON.stringify({ tenant_id, location_id, label })
			});
			if (res.status === 201) {
				const body = (await res.json()) as { id: string; label: string; secret: string };
				return { deviceCreated: true, deviceId: body.id, deviceLabel: body.label, secret: body.secret };
			}
			return fail(res.status, { error: `Failed to create device (HTTP ${res.status}).` });
		} catch {
			return fail(500, { error: 'Failed to create device due to a network or server error.' });
		}
	},

	toggleActive: async (event) => {
		const data = await event.request.formData();
		const device_id = data.get('device_id') as string;
		const is_active = data.get('is_active') === 'true';

		if (!device_id) {
			return fail(400, { error: 'Missing device id.' });
		}

		const key = event.platform?.env?.PLATFORM_ADMIN_KEY;
		if (!key) {
			return fail(500, { error: 'Console not configured (admin key missing).' });
		}

		const apiBase = getApiBase(event.platform);
		try {
			const res = await event.fetch(`${apiBase}/v1/admin/devices/${device_id}`, {
				method: 'PATCH',
				headers: { 'Content-Type': 'application/json', 'X-Platform-Admin-Key': key },
				body: JSON.stringify({ is_active })
			});
			if (res.ok) {
				return { toggled: true };
			}
			return fail(res.status, { error: `Failed to update device (HTTP ${res.status}).` });
		} catch {
			return fail(500, { error: 'Failed to update device due to a network or server error.' });
		}
	},

	addButton: async (event) => {
		const data = await event.request.formData();
		const device_id = data.get('device_id') as string;
		const button_code = (data.get('button_code') as string)?.trim() || '';
		const label = (data.get('label') as string)?.trim() || '';
		const staff_member_id = (data.get('staff_member_id') as string)?.trim() || '';

		if (!device_id || !button_code) {
			return fail(400, { error: 'Button code is required.' });
		}

		const key = event.platform?.env?.PLATFORM_ADMIN_KEY;
		if (!key) {
			return fail(500, { error: 'Console not configured (admin key missing).' });
		}

		const apiBase = getApiBase(event.platform);
		try {
			const res = await event.fetch(`${apiBase}/v1/admin/devices/${device_id}/buttons`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', 'X-Platform-Admin-Key': key },
				body: JSON.stringify({
					button_code,
					label: label || undefined,
					staff_member_id: staff_member_id || undefined
				})
			});
			if (res.status === 201) {
				return { buttonAdded: true };
			}
			return fail(res.status, { error: `Failed to add button (HTTP ${res.status}).` });
		} catch {
			return fail(500, { error: 'Failed to add button due to a network or server error.' });
		}
	}
};
