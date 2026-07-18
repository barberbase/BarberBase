<script lang="ts">
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import Seo from '$lib/components/Seo.svelte';
	import { resolve } from '$app/paths';
	import { BRAND } from '$lib/site-config';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
</script>

<Seo
	title="Case Studies — {BRAND}"
	description="How barbershops use {BRAND} to run their queue."
	path="/case-studies"
/>

<div class="min-h-screen bg-canvas text-primary font-manrope flex flex-col">
	<SiteHeader activePage="/case-studies" />

	<main id="main-content" class="flex-grow w-full max-w-3xl mx-auto px-6 py-12 md:py-20">
		<h1 class="font-satoshi font-extrabold text-3xl md:text-4xl tracking-[-0.03em] mb-10" style="text-wrap: balance;">
			Case Studies
		</h1>

		{#if data.caseStudies.length === 0}
			<p class="text-muted">Case studies coming soon.</p>
		{:else}
			<div class="space-y-px rounded-xl overflow-hidden bg-white/[0.03]">
				{#each data.caseStudies as study}
					<a
						href={resolve('/case-studies/[slug]', { slug: study.slug })}
						class="block bg-canvas p-6 hover:bg-white/[0.01] transition-colors"
					>
						<h2 class="font-satoshi font-bold text-lg mb-1">{study.title}</h2>
						<p class="text-sm text-muted leading-relaxed max-w-[65ch]">{study.description}</p>
					</a>
				{/each}
			</div>
		{/if}
	</main>

	<SiteFooter />
</div>
