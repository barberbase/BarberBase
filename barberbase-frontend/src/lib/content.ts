import { marked } from 'marked';

export interface Post {
	slug: string;
	title: string;
	description: string;
	date: string;
	ogImage?: string;
	draft: boolean;
	html: string;
}

function parseFrontmatter(raw: string): { meta: Record<string, string>; body: string } {
	const match = raw.match(/^---\n([\s\S]*?)\n---\n?([\s\S]*)$/);
	if (!match) return { meta: {}, body: raw };
	const meta: Record<string, string> = {};
	for (const line of match[1].split('\n')) {
		const i = line.indexOf(':');
		if (i === -1) continue;
		const key = line.slice(0, i).trim();
		let value = line.slice(i + 1).trim();
		if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
			value = value.slice(1, -1);
		}
		meta[key] = value;
	}
	return { meta, body: match[2] };
}

function loadCollection(files: Record<string, string>): Post[] {
	const posts = Object.entries(files).map(([path, raw]) => {
		const slug = path.split('/').pop()!.replace(/\.md$/, '');
		const { meta, body } = parseFrontmatter(raw);
		return {
			slug,
			title: meta.title ?? slug,
			description: meta.description ?? '',
			date: meta.date ?? '',
			ogImage: meta.ogImage,
			draft: meta.draft === 'true',
			html: marked.parse(body, { async: false }) as string
		};
	});
	return posts.sort((a, b) => (a.date < b.date ? 1 : -1));
}

const blogFiles = import.meta.glob('/src/content/blog/*.md', {
	eager: true,
	query: '?raw',
	import: 'default'
}) as Record<string, string>;

const caseStudyFiles = import.meta.glob('/src/content/case-studies/*.md', {
	eager: true,
	query: '?raw',
	import: 'default'
}) as Record<string, string>;

export const allBlogPosts = loadCollection(blogFiles);
export const allCaseStudies = loadCollection(caseStudyFiles);

export const publishedBlogPosts = allBlogPosts.filter((p) => !p.draft);
export const publishedCaseStudies = allCaseStudies.filter((p) => !p.draft);
