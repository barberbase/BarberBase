<script lang="ts">
	import { enhance } from '$app/forms';
	import Icon from '$lib/components/Icon.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import type { PageData, ActionData } from './$types';

	let { data, form }: { data: PageData; form: ActionData } = $props();

	const DAY_NAMES = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

	// Editable copy of the schedule; server returns all 7 days (closed by default).
	let days = $state(
		Array.from({ length: 7 }, (_, d) => {
			const existing = (data.days as any[])?.find((x) => x.day_of_week === d);
			return {
				day_of_week: d,
				is_open: existing?.is_open ?? false,
				opens_at: existing?.opens_at ?? '09:00',
				closes_at: existing?.closes_at ?? '21:00'
			};
		})
	);

	let saving = $state(false);

	function copyToAll(source: number) {
		const src = days[source];
		for (const d of days) {
			if (d.day_of_week !== source && d.is_open) {
				d.opens_at = src.opens_at;
				d.closes_at = src.closes_at;
			}
		}
	}
</script>

<svelte:head>
	<title>Business Hours — Admin — BarberBase</title>
	<meta name="description" content="Set weekly opening and closing times for your shop" />
</svelte:head>

<div class="min-h-dvh bg-canvas">
	<div class="max-w-2xl mx-auto p-4 sm:p-6">
		<div class="flex items-center gap-3 mb-2">
			<a
				href="/admin"
				class="text-muted hover:text-primary transition-colors text-sm min-h-[44px] inline-flex items-center"
				>← Admin</a
			>
			<span class="text-dim">/</span>
			<h1 class="text-2xl font-bold text-primary">Business Hours</h1>
		</div>
		<p class="text-muted text-sm mb-6">
			Weekly schedule shown to customers. Manual overrides from
			<a href="/admin/shop" class="text-gold-accent hover:underline">Shop Status</a> take priority over
			this schedule.
		</p>

		{#if form?.error}
			<div
				class="bg-system-error/10 border border-system-error/30 rounded-xl p-4 mb-6 text-system-error text-sm"
				role="alert"
			>
				{form.error}
			</div>
		{/if}
		{#if form?.success}
			<div
				class="bg-system-success/10 border border-system-success/30 rounded-xl p-4 mb-6 text-system-success text-sm flex items-center gap-2"
				role="status"
			>
				<Icon name="check" size={14} /> Business hours saved
			</div>
		{/if}

		<form
			method="POST"
			action="?/save"
			use:enhance={() => {
				saving = true;
				return async ({ update }) => {
					saving = false;
					await update({ reset: false });
				};
			}}
		>
			<input type="hidden" name="location_id" value={data.locationId} />

			<Card.Root class="rounded-2xl border-white/[0.05] py-0 gap-0">
				<Card.Header class="border-b border-white/[0.04] px-4 sm:px-5 py-4 gap-0">
					<Card.Title class="text-base text-primary">Weekly schedule</Card.Title>
					<Card.Description class="text-xs text-muted"
						>Toggle a day off to mark it closed.</Card.Description
					>
				</Card.Header>
				<Card.Content class="p-0 divide-y divide-white/[0.04]">
					{#each days as day, i}
						<div class="px-4 sm:px-5 py-3 flex flex-col sm:flex-row sm:items-center gap-3">
							<div class="flex items-center gap-3 sm:w-44 shrink-0 min-h-[44px]">
								<Switch
									id="day-{day.day_of_week}"
									name="open_{day.day_of_week}"
									bind:checked={day.is_open}
								/>
								<Label
									for="day-{day.day_of_week}"
									class="text-sm font-bold cursor-pointer {day.is_open
										? 'text-primary'
										: 'text-dim'}"
								>
									{DAY_NAMES[day.day_of_week]}
								</Label>
							</div>

							{#if day.is_open}
								<div class="flex items-center gap-2 flex-1">
									<input
										type="time"
										name="opens_{day.day_of_week}"
										bind:value={day.opens_at}
										required
										aria-label="{DAY_NAMES[day.day_of_week]} opening time"
										class="bg-titanium border border-white/[0.05] rounded-lg px-2.5 py-2 text-primary text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gold-accent [color-scheme:dark] min-h-[44px]"
									/>
									<span class="text-dim text-xs">to</span>
									<input
										type="time"
										name="closes_{day.day_of_week}"
										bind:value={day.closes_at}
										required
										aria-label="{DAY_NAMES[day.day_of_week]} closing time"
										class="bg-titanium border border-white/[0.05] rounded-lg px-2.5 py-2 text-primary text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gold-accent [color-scheme:dark] min-h-[44px]"
									/>
									<Button
										type="button"
										variant="ghost"
										size="sm"
										class="ml-auto text-xs text-muted hover:text-gold-accent min-h-[44px]"
										onclick={() => copyToAll(i)}
										title="Copy these times to all open days"
									>
										Copy to all
									</Button>
								</div>
							{:else}
								<Badge
									variant="outline"
									class="text-dim border-white/[0.06] font-mono text-[10px] uppercase tracking-widest sm:ml-2"
								>
									Closed
								</Badge>
							{/if}
						</div>
					{/each}
				</Card.Content>
			</Card.Root>

			<Button
				type="submit"
				disabled={saving}
				class="mt-6 w-full min-h-[48px] rounded-xl font-bold text-canvas hover:brightness-110 active:brightness-90 active:scale-[0.98] transition-all"
			>
				{saving ? 'Saving…' : 'Save Hours'}
			</Button>
		</form>
	</div>
</div>
