import { redirect } from '@sveltejs/kit';
import type { LayoutServerLoad } from './$types';
import { decodeToken, isTokenExpired } from '../../lib/api/client';

export const load: LayoutServerLoad = ({ cookies }) => {
	const accessToken = cookies.get('access_token');
	if (!accessToken) {
		throw redirect(302, '/login');
	}

	const claims = decodeToken(accessToken);
	if (!claims || isTokenExpired(claims)) {
		throw redirect(302, '/login');
	}

	return {
		staff: claims
	};
};
