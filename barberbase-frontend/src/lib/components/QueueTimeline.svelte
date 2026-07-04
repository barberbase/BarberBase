<script lang="ts">
	import Icon from './Icon.svelte';

	// Vertical journey timeline for q/status. Maps state + presence_state from
	// GET /queue/my-status onto the happy path from 10_customer_journey.md:
	// remote/notified → on_the_way → arrived → called → in_progress → completed.
	// Interrupts (snoozed/skipped/no_show/cancelled/expired) freeze progress at
	// the last reached step and render as a side-branch, not forward progress.
	interface Props {
		state: string;
		presenceState: string | null;
	}

	let { state, presenceState }: Props = $props();

	const steps = ['Joined', 'On the Way', 'Arrived', 'Called', 'In Service', 'Done'];

	const interruptLabels: Record<string, string> = {
		snoozed: 'Spot Paused',
		skipped: 'Spot Paused',
		no_show: 'Spot Paused',
		cancelled: 'Cancelled',
		expired: 'Expired'
	};

	let interrupt = $derived(
		interruptLabels[state] ?? (presenceState === 'snoozed' ? interruptLabels.snoozed : null)
	);

	// Index of the currently active step; steps.length means every step is done.
	let active = $derived.by(() => {
		if (state === 'completed') return steps.length;
		if (state === 'in_progress') return 4;
		if (state === 'called') return 3;
		if (presenceState === 'arrived') return 2;
		if (presenceState === 'on_the_way') return 1;
		return 0; // remote / notified
	});
</script>

<ol class="space-y-0" aria-label="Queue progress">
	{#each steps as label, i}
		{@const done = i < active}
		{@const current = i === active && !interrupt}
		<li class="flex items-center gap-3 relative min-h-[34px]">
			<!-- connector to the next step; colored only once this step is done -->
			{#if i < steps.length - 1}
				<span
					class="absolute left-[11px] top-[23px] bottom-[-11px] w-px {done
						? 'bg-gold-accent/40'
						: 'bg-white/[0.07]'}"
				></span>
			{/if}

			<span
				class="w-[23px] h-[23px] shrink-0 rounded-full flex items-center justify-center border
					{done
					? 'border-gold-accent/40 bg-gold-accent/10 text-gold-accent'
					: current
						? 'border-gold-accent bg-gold-accent/15 text-gold-accent shadow-[0_0_10px_rgba(200,169,107,0.25)]'
						: 'border-white/10 bg-canvas text-dim'}"
			>
				{#if done}
					<Icon name="check" size={11} />
				{:else if current}
					<span class="w-[9px] h-[9px] rounded-full bg-gold-accent motion-safe:animate-pulse"></span>
				{:else}
					<span class="w-[5px] h-[5px] rounded-full bg-white/10"></span>
				{/if}
			</span>

			<span
				class="font-mono text-[11px] uppercase tracking-[0.15em]
					{done ? 'text-muted' : current ? 'text-gold-accent font-bold' : 'text-dim'}"
				aria-current={current ? 'step' : undefined}
			>
				{label}
			</span>
		</li>

		<!-- interrupt branches sideways off the last reached step -->
		{#if interrupt && i === active}
			<li class="flex items-center gap-3 relative min-h-[34px] pl-9">
				<span class="absolute left-[11px] top-[-11px] h-[28px] w-px bg-system-warning/40"></span>
				<span class="absolute left-[11px] top-[17px] w-[18px] h-px bg-system-warning/40"></span>
				<span
					class="w-[23px] h-[23px] shrink-0 rounded-full flex items-center justify-center border border-system-warning/40 bg-system-warning/10 text-system-warning"
				>
					<Icon name={state === 'cancelled' || state === 'expired' ? 'x-circle' : 'pause-circle'} size={13} />
				</span>
				<span class="font-mono text-[11px] uppercase tracking-[0.15em] text-system-warning font-bold">
					{interrupt}
				</span>
			</li>
		{/if}
	{/each}
</ol>
