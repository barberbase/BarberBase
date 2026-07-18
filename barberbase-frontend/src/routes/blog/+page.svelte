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
	title="Blog — {BRAND}"
	description="Notes on running a walk-in barbershop queue — from the team building {BRAND}."
	path="/blog"
/>

<div class="min-h-screen bg-canvas text-primary font-manrope flex flex-col">
	<SiteHeader activePage="/blog" />

	<main id="main-content" class="flex-grow w-full max-w-3xl mx-auto px-6 py-12 md:py-20">
		<h1 class="font-satoshi font-extrabold text-3xl md:text-4xl tracking-[-0.03em] mb-10" style="text-wrap: balance;">
			Blog
		</h1>

		{#if data.posts.length === 0}
			<p class="text-muted">No posts yet.</p>
		{:else}
			<div class="space-y-px rounded-xl overflow-hidden bg-white/[0.03]">
				{#each data.posts as post}
					<a
						href={resolve('/blog/[slug]', { slug: post.slug })}
						class="block bg-canvas p-6 hover:bg-white/[0.01] transition-colors"
					>
						<time class="text-xs text-dim font-mono uppercase tracking-widestUI">{post.date}</time>
						<h2 class="font-satoshi font-bold text-lg mt-2 mb-1">{post.title}</h2>
						<p class="text-sm text-muted leading-relaxed max-w-[65ch]">{post.description}</p>
					</a>
				{/each}
			</div>
		{/if}

		{#if data.totalPages > 1}
			<nav class="flex justify-center gap-3 mt-10 text-sm">
				{#if data.page > 1}
					<a href="/blog?page={data.page - 1}" class="text-muted hover:text-primary transition-colors">Previous</a>
				{/if}
				<span class="text-dim">Page {data.page} of {data.totalPages}</span>
				{#if data.page < data.totalPages}
					<a href="/blog?page={data.page + 1}" class="text-muted hover:text-primary transition-colors">Next</a>
				{/if}
			</nav>
		{/if}
	</main>

	<SiteFooter />
</div>
