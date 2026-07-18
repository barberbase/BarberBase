<script lang="ts">
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import Seo from '$lib/components/Seo.svelte';
	import { LEGAL_ENTITY_NAME, SITE_URL } from '$lib/site-config';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	const articleJsonLd = $derived({
		'@context': 'https://schema.org',
		'@type': 'Article',
		headline: data.study.title,
		description: data.study.description,
		datePublished: data.study.date,
		url: `${SITE_URL}/case-studies/${data.study.slug}`,
		author: { '@type': 'Organization', name: LEGAL_ENTITY_NAME }
	});
</script>

<Seo
	title="{data.study.title} — Case Studies"
	description={data.study.description}
	path="/case-studies/{data.study.slug}"
	type="article"
	noindex={data.study.draft}
/>

<svelte:head>
	{@html `<script type="application/ld+json">${JSON.stringify(articleJsonLd).replace(/</g, '\\u003c')}</script>`}
</svelte:head>

<div class="min-h-screen bg-canvas text-primary font-manrope flex flex-col">
	<SiteHeader activePage="/case-studies" />

	<main id="main-content" class="flex-grow w-full max-w-3xl mx-auto px-6 py-12 md:py-20">
		<h1 class="font-satoshi font-extrabold text-3xl md:text-4xl tracking-[-0.03em] mb-8" style="text-wrap: balance;">
			{data.study.title}
		</h1>
		<article class="article-prose prose prose-invert max-w-none">
			{@html data.study.html}
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
