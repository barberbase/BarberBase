<script lang="ts">
	import { enhance } from '$app/forms';
	import Icon from '$lib/components/Icon.svelte';
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

<div class="min-h-screen bg-canvas">
	<div class="max-w-2xl mx-auto p-6">
		<div class="flex items-center gap-3 mb-2">
			<a href="/admin" class="text-muted hover:text-primary transition-colors text-sm">← Admin</a>
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
			>
				{form.error}
			</div>
		{/if}
		{#if form?.success}
			<div
				class="bg-system-success/10 border border-system-success/30 rounded-xl p-4 mb-6 text-system-success text-sm flex items-center gap-2"
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

			<div class="bg-matte border border-white/[0.05] rounded-2xl shadow-xl divide-y divide-white/[0.04]">
				{#each days as day, i}
					<div class="p-4 flex flex-col sm:flex-row sm:items-center gap-3">
						<label class="flex items-center gap-3 sm:w-40 shrink-0 cursor-pointer">
							<input
								type="checkbox"
								name="open_{day.day_of_week}"
								bind:checked={day.is_open}
								class="h-4 w-4 rounded border-white/20 bg-titanium text-gold-accent focus:ring-gold-accent focus:ring-offset-0"
							/>
							<span class="text-sm font-bold {day.is_open ? 'text-primary' : 'text-dim'}">
								{DAY_NAMES[day.day_of_week]}
							</span>
						</label>

						{#if day.is_open}
							<div class="flex items-center gap-2 flex-1">
								<input
									type="time"
									name="opens_{day.day_of_week}"
									bind:value={day.opens_at}
									required
									class="bg-titanium border border-white/[0.05] rounded-lg px-2.5 py-2 text-primary text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gold-accent [color-scheme:dark]"
								/>
								<span class="text-dim text-xs">to</span>
								<input
									type="time"
									name="closes_{day.day_of_week}"
									bind:value={day.closes_at}
									required
									class="bg-titanium border border-white/[0.05] rounded-lg px-2.5 py-2 text-primary text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gold-accent [color-scheme:dark]"
								/>
								<button
									type="button"
									onclick={() => copyToAll(i)}
									class="ml-auto text-xs text-muted hover:text-gold-accent transition-colors whitespace-nowrap"
									title="Copy these times to all open days"
								>
									Copy to all
								</button>
							</div>
						{:else}
							<span class="text-xs text-dim sm:ml-2">Closed</span>
						{/if}
					</div>
				{/each}
			</div>

			<button
				type="submit"
				disabled={saving}
				class="mt-6 w-full bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] disabled:opacity-40 disabled:cursor-not-allowed text-canvas font-bold py-3 rounded-xl transition-all duration-150"
			>
				{saving ? 'Saving…' : 'Save Hours'}
			</button>
		</form>
	</div>
</div>
