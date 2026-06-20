import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { signSession, constantTimeEqual, verifySession } from '$lib/server/platformSession';

export const load: PageServerLoad = async ({ cookies, platform }) => {
	const secret = platform?.env?.PLATFORM_ADMIN_KEY;
	const session = cookies.get('platform_session');
	if (secret && session && (await verifySession(session, secret))) {
		throw redirect(303, '/platform');
	}
	return {};
};

export const actions: Actions = {
	login: async ({ request, platform, cookies }) => {
		const data = await request.formData();
		const password = data.get('password') as string;

		const expected = platform?.env?.PLATFORM_OPERATOR_PASSWORD;
		if (!expected) {
			return fail(500, { error: 'Console not configured (operator password missing).' });
		}

		if (!password || !constantTimeEqual(password, expected)) {
			return fail(401, { error: 'Incorrect password.' });
		}

		const secret = platform?.env?.PLATFORM_ADMIN_KEY;
		if (!secret) {
			return fail(500, { error: 'Console not configured (admin key missing).' });
		}

		const token = await signSession(secret, 8 * 3600);

		cookies.set('platform_session', token, {
			httpOnly: true,
			secure: true,
			path: '/',
			sameSite: 'lax',
			maxAge: 8 * 3600
		});

		throw redirect(303, '/platform');
	}
};
