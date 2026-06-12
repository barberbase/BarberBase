<script lang="ts">
	import PushPermissionPrompt from '$lib/components/PushPermissionPrompt.svelte';

	let { data, children } = $props<{ data: { jwt: string }; children: import('svelte').Snippet }>();
</script>

<svelte:head>
	<link rel="manifest" href="/dashboard/manifest.json" />
</svelte:head>

{@render children()}
<PushPermissionPrompt jwt={data.jwt} />
