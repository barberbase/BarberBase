<script lang="ts">
	import { slide } from 'svelte/transition';
	import { cubicOut } from 'svelte/easing';
	import { resolve } from '$app/paths';
	import logo from '$lib/assets/favicon.svg';
	import { BRAND } from '$lib/site-config';
	import Button from '$lib/components/Button.svelte';

	let { activePage = '' }: { activePage?: string } = $props();

	let mobileMenuOpen = $state(false);

	const links: { href: '/' | '/about' | '/terms' | '/contact'; label: string }[] = [
		{ href: '/about', label: 'About' },
		{ href: '/terms', label: 'Terms' },
		{ href: '/contact', label: 'Contact' }
	];
</script>

<header class="w-full max-w-6xl mx-auto px-6 py-6 flex justify-between items-center">
	<a href={resolve('/')} class="flex items-center space-x-3">
		<img src={logo} alt="BarberBase Logo" class="h-8 w-8" />
		<span class="font-satoshi font-extrabold text-2xl tracking-widestUI text-gold-accent">{BRAND}</span>
	</a>
	<div class="flex items-center space-x-6">
		<nav class="hidden md:flex space-x-6 text-sm font-medium text-muted">
			{#each links as link}
				<a
					href={resolve(link.href)}
					class={activePage === link.href ? 'text-primary' : 'hover:text-primary transition-colors'}
				>{link.label}</a>
			{/each}
		</nav>
		<a href={resolve('/login')} class="hidden md:inline-block w-32">
			<Button variant="secondary" class="py-2.5 px-5 text-[11px] font-semibold border-white/10">Staff Login</Button>
		</a>
		<button class="md:hidden p-2 text-muted hover:text-primary transition-colors" onclick={() => mobileMenuOpen = !mobileMenuOpen} aria-label="Toggle menu">
			<svg width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true">
				{#if mobileMenuOpen}
					<path d="M5 5l10 10M15 5L5 15" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
				{:else}
					<path d="M3 6h14M3 10h14M3 14h14" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
				{/if}
			</svg>
		</button>
	</div>
</header>

{#if mobileMenuOpen}
	<nav transition:slide={{ duration: 200, easing: cubicOut }} class="md:hidden bg-matte border-b border-white/[0.03] px-6 py-4 flex flex-col gap-3 text-sm font-medium text-muted">
		{#each links as link}
			<a
				href={resolve(link.href)}
				class="{activePage === link.href ? 'text-primary' : 'hover:text-primary transition-colors'} py-2.5"
			>{link.label}</a>
		{/each}
		<a href={resolve('/login')} class="hover:text-primary transition-colors py-2.5">Staff Login</a>
	</nav>
{/if}
