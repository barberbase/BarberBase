import type { RequestHandler } from './$types';
import { SITE_URL } from '$lib/site-config';
import { publishedBlogPosts, publishedCaseStudies } from '$lib/content';

const STATIC_PATHS = [
	'/',
	'/about',
	'/contact',
	'/demo',
	'/privacy',
	'/terms',
	'/blog',
	'/resources',
	'/case-studies'
];

// TODO(shop enumeration): once GET /public/locations ({slug, name, updated_at} for
// indexable shops) ships as its own backend unit, fetch it here and push a
// `/<slug>` <url> entry per shop into `entries` below, e.g.:
//
//   const res = await fetch(`${PUBLIC_API_BASE}/v1/public/locations`);
//   const shops = await res.json();
//   for (const shop of shops) entries.push({ loc: `${SITE_URL}/${shop.slug}`, lastmod: shop.updated_at });
//
// No known-good production shop slug exists to hardcode in the meantime.

export const GET: RequestHandler = () => {
	const entries: { loc: string; lastmod?: string }[] = STATIC_PATHS.map((path) => ({
		loc: `${SITE_URL}${path}`
	}));

	for (const post of publishedBlogPosts) {
		entries.push({ loc: `${SITE_URL}/blog/${post.slug}`, lastmod: post.date });
	}
	for (const study of publishedCaseStudies) {
		entries.push({ loc: `${SITE_URL}/case-studies/${study.slug}`, lastmod: study.date });
	}

	const urls = entries
		.map(
			(e) =>
				`\t<url>\n\t\t<loc>${e.loc}</loc>${e.lastmod ? `\n\t\t<lastmod>${e.lastmod}</lastmod>` : ''}\n\t</url>`
		)
		.join('\n');

	const body = `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls}\n</urlset>`;

	return new Response(body, {
		headers: { 'Content-Type': 'application/xml' }
	});
};
