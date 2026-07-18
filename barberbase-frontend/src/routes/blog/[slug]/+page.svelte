<script lang="ts">
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import Seo from '$lib/components/Seo.svelte';
	import { LEGAL_ENTITY_NAME, SITE_URL } from '$lib/site-config';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	const articleJsonLd = $derived({
		'@context': 'https://schema.org',
		'@type': 'BlogPosting',
		headline: data.post.title,
		description: data.post.description,
		datePublished: data.post.date,
		url: `${SITE_URL}/blog/${data.post.slug}`,
		author: { '@type': 'Organization', name: LEGAL_ENTITY_NAME }
	});
</script>

<Seo
	title="{data.post.title} — Blog"
	description={data.post.description}
	path="/blog/{data.post.slug}"
	type="article"
	noindex={data.post.draft}
/>

<svelte:head>
	{@html `<script type="application/ld+json">${JSON.stringify(articleJsonLd).replace(/</g, '\\u003c')}</script>`}
</svelte:head>

<div class="min-h-screen bg-canvas text-primary font-manrope flex flex-col">
	<SiteHeader activePage="/blog" />

	<main id="main-content" class="flex-grow w-full max-w-3xl mx-auto px-6 py-12 md:py-20">
		<time class="text-xs text-dim font-mono uppercase tracking-widestUI">{data.post.date}</time>
		<h1 class="font-satoshi font-extrabold text-3xl md:text-4xl tracking-[-0.03em] mt-2 mb-8" style="text-wrap: balance;">
			{data.post.title}
		</h1>
		<article class="article-prose prose prose-invert max-w-none">
			{@html data.post.html}
		</article>
	</main>

	<SiteFooter />
</div>

<style>
	.article-prose {
		--tw-prose-body: var(--color-muted);
		--tw-prose-headings: var(--color-primary);
		--tw-prose-bold: var(--color-primary);
		--tw-prose-links: var(--color-gold-accent);
		--tw-prose-bullets: var(--color-dim);
		--tw-prose-hr: color-mix(in srgb, white 3%, transparent);
		--tw-prose-quotes: var(--color-primary);
		--tw-prose-quote-borders: var(--color-gold-accent);
	}
</style>
