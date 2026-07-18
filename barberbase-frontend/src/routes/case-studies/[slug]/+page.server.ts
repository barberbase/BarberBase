import { error } from '@sveltejs/kit';
import { allCaseStudies } from '$lib/content';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = ({ params }) => {
	const study = allCaseStudies.find((p) => p.slug === params.slug);
	if (!study) throw error(404, 'Case study not found');
	return { study };
};
