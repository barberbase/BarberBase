import { fail } from '@sveltejs/kit';
import type { Actions } from './$types';
import { getApiBase } from '$lib/api/client';

export const actions: Actions = {
	provision: async (event) => {
		const data = await event.request.formData();

		const tenant_name = (data.get('tenant_name') as string)?.trim() || '';
		const tenant_slug = (data.get('tenant_slug') as string)?.trim() || '';
		const owner_name = (data.get('owner_name') as string)?.trim() || '';
		let owner_phone = (data.get('owner_phone') as string)?.trim() || '';
		const location_name = (data.get('location_name') as string)?.trim() || '';
		const location_slug = (data.get('location_slug') as string)?.trim() || '';
		const address = (data.get('address') as string)?.trim() || undefined;
		const timezone = (data.get('timezone') as string)?.trim() || 'Asia/Kolkata';

		// Normalize owner_phone
		if (owner_phone.length === 10 && /^\d+$/.test(owner_phone)) {
			owner_phone = `+91${owner_phone}`;
		}

		// Validation
		if (!tenant_name) {
			return fail(400, { error: 'Tenant name is required.' });
		}
		if (!tenant_slug || !/^[a-z0-9-]+$/.test(tenant_slug)) {
			return fail(400, {
				error:
					'Tenant slug is required and must contain only lowercase letters, numbers, and hyphens.'
			});
		}
		if (!owner_name) {
			return fail(400, { error: 'Owner name is required.' });
		}
		if (!owner_phone || !/^\+91\d{10}$/.test(owner_phone)) {
			return fail(400, {
				error: 'Owner phone must be a valid E.164 phone number (e.g. +919876543210).'
			});
		}
		if (!location_name) {
			return fail(400, { error: 'Location name is required.' });
		}
		if (!location_slug || !/^[a-z0-9-]+\/[a-z0-9-]+$/.test(location_slug)) {
			return fail(400, {
				error: 'Location slug is required and must match "tenant-slug/location-slug" format.'
			});
		}
		if (!location_slug.startsWith(tenant_slug + '/')) {
			return fail(400, {
				error:
					'Location slug must start with the tenant slug followed by a slash (e.g. tenant-slug/location-slug).'
			});
		}

		const key = event.platform?.env?.PLATFORM_ADMIN_KEY;
		if (!key) {
			return fail(500, { error: 'Console not configured (admin key missing).' });
		}

		const apiBase = getApiBase(event.platform);

		try {
			const res = await event.fetch(`${apiBase}/v1/admin/setup`, {
				method: 'POST',
				headers: {
					'Content-Type': 'application/json',
					'X-Platform-Admin-Key': key
				},
				body: JSON.stringify({
					tenant_name,
					tenant_slug,
					owner_name,
					owner_phone,
					location_name,
					location_slug,
					address,
					timezone
				})
			});

			if (res.status === 201) {
				const body = (await res.json()) as any;
				return {
					success: true,
					tenant_id: body.tenant_id,
					location_id: body.location_id,
					owner_staff_member_id: body.owner_staff_member_id,
					arrival_pin: body.arrival_pin,
					owner_phone,
					public_path: '/' + location_slug
				};
			} else if (res.status === 409) {
				return fail(409, {
					error: 'A shop with that tenant slug, location slug, or owner phone already exists.'
				});
			} else if (res.status === 401) {
				return fail(500, {
					error: 'Admin key rejected by API — check PLATFORM_ADMIN_KEY env.'
				});
			} else {
				let errMessage = 'Provisioning failed. Try again.';
				try {
					const errBody = (await res.json()) as any;
					if (errBody?.message) errMessage = errBody.message;
				} catch {
					// Fallback to default message
				}
				return fail(500, { error: errMessage });
			}
		} catch (err) {
			return fail(500, { error: 'Provisioning failed due to a network or server error.' });
		}
	}
};
