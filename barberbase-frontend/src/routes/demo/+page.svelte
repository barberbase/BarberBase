<script lang="ts">
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import { resolve } from '$app/paths';
	import logo from '$lib/assets/favicon.svg';
	import { BRAND } from '$lib/site-config';
	import Button from '$lib/components/Button.svelte';
</script>

<svelte:head>
	<title>Try Demo — {BRAND}</title>
</svelte:head>

<div class="min-h-screen bg-canvas text-primary font-manrope flex flex-col relative overflow-hidden">
	<header class="w-full max-w-6xl mx-auto px-6 py-6 flex justify-between items-center relative z-10 border-b border-white/[0.03]">
		<a href={resolve('/')} class="flex items-center space-x-3 font-satoshi font-extrabold text-2xl tracking-widestUI text-gold-accent">
			<img src={logo} alt="BarberBase Logo" class="h-8 w-8" />
			<span>{BRAND}</span>
		</a>
		<a href={resolve('/')} class="text-sm font-semibold text-muted hover:text-primary transition-colors">&larr; Back</a>
	</header>

	<main class="flex-grow relative z-10 w-full max-w-3xl mx-auto px-6 py-14 md:py-20 text-center space-y-8">
		<h1 class="font-satoshi font-extrabold text-4xl md:text-5xl tracking-[-0.03em] leading-[1.08]" style="text-wrap: balance;">
			Run the demo in under a minute.
		</h1>
		<p class="text-muted max-w-2xl mx-auto leading-relaxed">
			Check customer join, live queue movement, and turn alerts in one quick walkthrough.
		</p>
		<div class="flex flex-col sm:flex-row gap-3 justify-center">
			<a href={resolve('/demo/playwright')} class="w-full sm:w-auto sm:min-w-64">
				<Button variant="primary">Launch demo</Button>
			</a>
			<a href={resolve('/')} class="w-full sm:w-auto sm:min-w-52">
				<Button variant="secondary">Back to homepage</Button>
			</a>
		</div>
	</main>

	<SiteFooter />
</div>
