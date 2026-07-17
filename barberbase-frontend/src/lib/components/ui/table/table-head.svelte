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

<th
	bind:this={ref}
	data-slot="table-head"
	class={cn(
		"text-muted h-10 px-4 text-left align-middle font-mono text-[10px] font-medium uppercase tracking-wider first:pl-6 last:pr-6 [&:has([role=checkbox])]:pr-0",
		className
	)}
	{...restProps}
>
	{@render children?.()}
</th>
