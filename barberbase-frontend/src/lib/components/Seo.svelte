<script lang="ts">
	import { SITE_URL } from '$lib/site-config';

	let {
		title,
		description,
		path = '/',
		image = '/icons/icon-512.png',
		type = 'website',
		noindex = false
	}: {
		title: string;
		description: string;
		path?: string;
		image?: string;
		type?: string;
		noindex?: boolean;
	} = $props();

	const url = `${SITE_URL}${path}`;
	const imageUrl = image.startsWith('http') ? image : `${SITE_URL}${image}`;
</script>

<svelte:head>
	<title>{title}</title>
	<meta name="description" content={description} />
	<link rel="manifest" href="/manifest.json" />
	<link rel="apple-touch-icon" href="/icons/icon-192.png" />
	{#if noindex}
		<meta name="robots" content="noindex" />
	{:else}
		<link rel="canonical" href={url} />
		<meta property="og:title" content={title} />
		<meta property="og:description" content={description} />
		<meta property="og:url" content={url} />
		<meta property="og:type" content={type} />
		<meta property="og:image" content={imageUrl} />
		<meta name="twitter:card" content="summary_large_image" />
		<meta name="twitter:title" content={title} />
		<meta name="twitter:description" content={description} />
		<meta name="twitter:image" content={imageUrl} />
	{/if}
</svelte:head>
