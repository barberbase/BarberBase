<script lang="ts">
	import type { HTMLAttributes } from "svelte/elements";
	import { cn, type WithElementRef } from "$lib/utils.js";

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLTableCellElement>> = $props();
</script>

<td
	bind:this={ref}
	data-slot="table-cell"
	class={cn(
		"text-primary px-4 py-4 align-middle text-sm first:pl-6 last:pr-6 [&:has([role=checkbox])]:pr-0",
		className
	)}
	{...restProps}
>
	{@render children?.()}
</td>
