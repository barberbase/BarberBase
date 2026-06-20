import { redirect } from '@sveltejs/kit';
import type { LayoutServerLoad } from './$types';
import { verifySession } from '$lib/server/platformSession';

export const load: LayoutServerLoad = async ({ cookies, url, platform }) => {
	if (url.pathname === '/platform/login') {
		return {};
	}

	const secret = platform?.env?.PLATFORM_ADMIN_KEY;
	if (!secret) {
		throw redirect(303, '/platform/login');
	}

	const session = cookies.get('platform_session');
	if (!session || !(await verifySession(session, secret))) {
		throw redirect(303, '/platform/login');
	}

	return {};
};
