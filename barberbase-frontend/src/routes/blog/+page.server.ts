import { publishedBlogPosts } from '$lib/content';
import type { PageServerLoad } from './$types';

const PAGE_SIZE = 10;

export const load: PageServerLoad = ({ url }) => {
	const page = Math.max(1, Number(url.searchParams.get('page')) || 1);
	const start = (page - 1) * PAGE_SIZE;
	const posts = publishedBlogPosts.slice(start, start + PAGE_SIZE);
	const totalPages = Math.max(1, Math.ceil(publishedBlogPosts.length / PAGE_SIZE));

	return { posts, page, totalPages };
};
