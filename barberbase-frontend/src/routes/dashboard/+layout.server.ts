import type { LayoutServerLoad } from './$types';

export const load: LayoutServerLoad = ({ cookies }) => {
	const jwt = cookies.get('access_token') ?? '';
	return { jwt };
};
