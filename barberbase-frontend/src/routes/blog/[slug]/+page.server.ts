import { error } from '@sveltejs/kit';
import { allBlogPosts } from '$lib/content';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = ({ params }) => {
	const post = allBlogPosts.find((p) => p.slug === params.slug);
	if (!post) throw error(404, 'Post not found');
	return { post };
};
