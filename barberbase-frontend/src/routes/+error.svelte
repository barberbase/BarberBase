<script lang="ts">
	import { page } from '$app/stores';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import { resolve } from '$app/paths';
	import logo from '$lib/assets/favicon.svg';
	import { BRAND } from '$lib/site-config';
</script>

<svelte:head>
	<title>Error — {BRAND}</title>
</svelte:head>

<div
	class="min-h-screen bg-canvas text-primary font-manrope flex flex-col relative overflow-hidden"
>
	<!-- Background Decorative Gradients -->
	<div class="absolute inset-0 overflow-hidden pointer-events-none z-0">
		<div
			class="absolute top-1/4 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[500px] h-[500px] rounded-full bg-gold-accent/10 blur-[120px]"
		></div>
		<div
			class="absolute bottom-1/4 left-1/3 w-[400px] h-[400px] rounded-full bg-blue-500/5 blur-[100px]"
		></div>
	</div>

	<!-- Navigation Bar -->
	<header
		class="w-full max-w-6xl mx-auto px-6 py-6 flex justify-between items-center relative z-10"
	>
		<a
			href={resolve('/')}
			class="flex items-center space-x-3 font-manrope font-extrabold text-2xl tracking-wider text-gold-accent"
		>
			<img src={logo} alt="BarberBase Logo" class="h-8 w-8" />
			<span>BarberBase</span>
		</a>
	</header>

	<main class="flex-grow flex items-center justify-center relative z-10 px-6 py-12 md:py-24">
		<div
			class="w-full max-w-md bg-matte/60 backdrop-blur-xl border border-white/[0.03] rounded-3xl p-8 shadow-2xl text-center space-y-6"
		>
			<div
				class="w-16 h-16 bg-gold-accent/10 rounded-full flex items-center justify-center text-gold-accent mx-auto"
			>
				<svg
					xmlns="http://www.w3.org/2000/svg"
					class="h-8 w-8"
					fill="none"
					viewBox="0 0 24 24"
					stroke="currentColor"
				>
					<path
						stroke-linecap="round"
						stroke-linejoin="round"
						stroke-width="2"
						d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
					/>
				</svg>
			</div>

			<div class="space-y-2">
				<h1 class="text-3xl font-extrabold text-primary font-manrope">
					{#if $page.status === 404}
						Link Incomplete
					{:else}
						Oops! Something went wrong
					{/if}
				</h1>
				<p class="text-sm text-muted font-mono">Status code: {$page.status}</p>
			</div>

			<p class="text-primary leading-relaxed text-sm md:text-base">
				{#if $page.status === 404}
					This link looks incomplete. Open the full link your barbershop shared, or ask them for it.
				{:else}
					An unexpected error occurred while loading this page. Please try again.
				{/if}
			</p>

			<div class="pt-4">
				<a
					href={resolve('/')}
					class="inline-flex items-center justify-center w-full py-3 bg-gold-accent hover:bg-amber-400 active:bg-amber-600 text-canvas font-bold text-sm rounded-xl transition-all shadow-md"
				>
					Back to Homepage
				</a>
			</div>
		</div>
	</main>

	<SiteFooter />
</div>
