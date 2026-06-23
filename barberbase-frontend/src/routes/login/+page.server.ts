import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { decodeToken, isTokenExpired, getApiBase } from '$lib/api/client';


export const load: PageServerLoad = async ({ cookies }) => {
	const accessToken = cookies.get('access_token');
	if (accessToken) {
		const claims = decodeToken(accessToken);
		if (claims && !isTokenExpired(claims)) {
			throw redirect(303, '/dashboard');
		}
	}
	return {};
};

export const actions: Actions = {
	requestOtp: async (event) => {
		const data = await event.request.formData();
		let phone_number = data.get('phone_number') as string;

		if (phone_number) {
			phone_number = phone_number.trim();
			if (phone_number.length === 10 && /^\d+$/.test(phone_number)) {
				phone_number = `+91${phone_number}`;
			}
		}

		if (!phone_number || !/^\+91\d{10}$/.test(phone_number)) {
			return fail(400, {
				error: 'Invalid phone number format. Must be +91 followed by 10 digits.',
				step: 'phone'
			});
		}

		const apiBase = getApiBase(event.platform);
		try {
			const res = await event.fetch(`${apiBase}/v1/auth/staff/request-otp`, {
				method: 'POST',
				headers: {
					'Content-Type': 'application/json'
				},
				body: JSON.stringify({ phone_number })
			});

			if (res.status === 200) {
				return { step: 'otp', phone_number };
			} else if (res.status === 429) {
				return fail(429, {
					error: 'Too many requests. Wait 10 minutes.',
					step: 'phone'
				});
			} else {
				return fail(500, {
					error: 'Could not send OTP. Try again.',
					step: 'phone'
				});
			}
		} catch (err) {
			return fail(500, {
				error: 'Could not send OTP. Try again.',
				step: 'phone'
			});
		}
	},

	verifyOtp: async (event) => {
		const data = await event.request.formData();
		const phone_number = data.get('phone_number') as string;
		const otp = data.get('otp') as string;

		if (!phone_number || !/^\+91\d{10}$/.test(phone_number)) {
			return fail(400, {
				error: 'Invalid phone number context. Please request OTP again.',
				step: 'phone'
			});
		}

		if (!otp || !/^\d{6}$/.test(otp)) {
			return fail(400, {
				error: 'Invalid OTP format. Must be a 6-digit code.',
				step: 'otp',
				phone_number
			});
		}

		let success = false;
		let bbAccess: string | null = null;
		let bbRefresh: string | null = null;
		let errorResponse: any = null;

		const apiBase = getApiBase(event.platform);
		try {
			const res = await event.fetch(`${apiBase}/v1/auth/staff/verify-otp`, {
				method: 'POST',
				headers: {
					'Content-Type': 'application/json'
				},
				body: JSON.stringify({ phone_number, otp })
			});

			if (res.status === 200) {
				const body = await res.json() as {
					access_token: string;
					refresh_token: string;
					staff_member_id: string;
					name: string;
					role: string;
					location_id: string;
					tenant_id: string;
				};
				bbAccess = body.access_token ?? null;
				bbRefresh = body.refresh_token ?? null;

				if (!bbAccess || !bbRefresh) {
					errorResponse = fail(500, {
						error: 'Login failed: tokens missing from server response.',
						step: 'otp',
						phone_number
					});
				} else {
					success = true;
				}
			} else if (res.status === 401) {
				errorResponse = fail(401, {
					error: 'Invalid or expired code.',
					step: 'otp',
					phone_number
				});
			} else {
				errorResponse = fail(500, {
					error: 'Login failed. Try again.',
					step: 'otp',
					phone_number
				});
			}
		} catch (err) {
			errorResponse = fail(500, {
				error: 'Login failed. Try again.',
				step: 'otp',
				phone_number
			});
		}

		if (success && bbAccess && bbRefresh) {
			event.cookies.set('access_token', bbAccess, {
				httpOnly: true,
				secure: true,
				path: '/',
				sameSite: 'lax'
			});

			event.cookies.set('refresh_token', bbRefresh, {
				httpOnly: true,
				secure: true,
				path: '/',
				sameSite: 'lax'
			});

			throw redirect(303, '/dashboard');
		}

		return errorResponse;
	}
};
