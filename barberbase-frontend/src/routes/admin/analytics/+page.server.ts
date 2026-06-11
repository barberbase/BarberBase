import { redirect } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';
import { ApiClient } from '$lib/api/client';
import type { components } from '$lib/api/client';

type DailyAnalytics = components['schemas']['DailyAnalytics'];

function makeClient(event: any) {
	const accessToken = event.cookies.get('access_token');
	const isTest = event.url.hostname === 'localhost' || event.url.hostname === '127.0.0.1';
	const apiBase = isTest ? 'http://127.0.0.1:9090' : undefined;
	const platformMock = apiBase ? { env: { PUBLIC_API_BASE: apiBase } } : event.platform;
	return new ApiClient(accessToken, platformMock);
}

export const load: PageServerLoad = async (event) => {
	const client = makeClient(event);

	// Date comes from query param — server reads it, never from client-side JS
	const dateParam = event.url.searchParams.get('date') ?? '';

	let analytics: DailyAnalytics | null = null;
	let analyticsError = '';
	try {
		const path = dateParam
			? `/v1/staff/analytics/daily?date=${encodeURIComponent(dateParam)}`
			: '/v1/staff/analytics/daily';
		analytics = await client.get<DailyAnalytics>(path);
	} catch (err: any) {
		if (err?.status === 401) throw redirect(302, '/login');
		analyticsError = err?.data?.message || 'Failed to load analytics';
	}

	return { analytics, analyticsError, selectedDate: dateParam };
};
