<script lang="ts">
  import ImpeccableLiveRoot from '$lib/impeccable/ImpeccableLiveRoot.svelte';
	/* BarberBase Frontend Root Layout */
	import './layout.css';
	import favicon from '$lib/assets/favicon.svg';

	let { children } = $props();
</script>

<svelte:head><link rel="icon" href={favicon} /></svelte:head>
<!-- impeccable-live-svelte-start -->
<ImpeccableLiveRoot />
<!-- impeccable-live-svelte-end -->
{@render children()}
