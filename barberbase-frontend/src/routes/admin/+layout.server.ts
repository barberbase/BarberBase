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

	// Role guard per spec: barber → /dashboard, anything else → /login
	if (claims.role === 'barber') {
		throw redirect(302, '/dashboard');
	}
	if (claims.role !== 'owner' && claims.role !== 'manager') {
		throw redirect(302, '/login');
	}

	return {
		staff: claims
	};
};
