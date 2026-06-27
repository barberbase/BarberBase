<script lang="ts">
  import ImpeccableLiveRoot from '$lib/impeccable/ImpeccableLiveRoot.svelte';
	/* BarberBase Frontend Root Layout */
	import './layout.css';
	import favicon from '$lib/assets/favicon.svg';

	let { children } = $props();
</script>

<svelte:head><link rel="icon" href={favicon} /></svelte:head>
<a href="#main-content" class="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-[60] focus:bg-gold-accent focus:text-canvas focus:px-4 focus:py-2 focus:rounded-lg focus:text-sm focus:font-bold">Skip to content</a>
<!-- impeccable-live-svelte-start -->
<ImpeccableLiveRoot />
<!-- impeccable-live-svelte-end -->
{@render children()}
