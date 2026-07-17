<script lang="ts">
	import { goto } from '$app/navigation';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	let selectedDate = $state(data.selectedDate || '');

	function formatRupees(paise: number | undefined | null): string {
		if (paise == null) return '₹0';
		return `₹${(paise / 100).toLocaleString('en-IN')}`;
	}

	function handleDateChange(e: Event) {
		const target = e.target as HTMLInputElement;
		const val = target.value;
		selectedDate = val;
		goto(val ? `/admin/analytics?date=${val}` : '/admin/analytics', { replaceState: true });
	}

	// Revenue-share bar: width relative to the top-earning barber for the day.
	const maxBarberRevenue = $derived(
		Math.max(0, ...(data.analytics?.barber_breakdown ?? []).map((row) => row.revenue_paise ?? 0))
	);
</script>

<svelte:head>
	<title>Analytics — Admin — BarberBase</title>
	<meta
		name="description"
		content="Daily revenue, visit counts, and barber performance analytics"
	/>
</svelte:head>

<div class="min-h-screen bg-canvas">
	<div class="max-w-4xl mx-auto p-6">
		<!-- Header -->
		<div class="flex items-center justify-between mb-6">
			<div class="flex items-center gap-3">
				<a href="/admin" class="text-muted hover:text-primary transition-colors text-sm"
					>← Admin</a
				>
				<span class="text-dim">/</span>
				<h1 class="text-2xl font-bold text-primary">Analytics</h1>
			</div>
			<!-- Date picker — changing reloads via goto -->
			<div>
				<label for="analytics-date-picker" class="sr-only">Select Date</label>
				<input
					id="analytics-date-picker"
					type="date"
					value={selectedDate}
					onchange={handleDateChange}
					class="bg-titanium border border-white/[0.05] rounded-xl px-3 py-2 text-primary text-sm focus:outline-none focus:ring-2 focus:ring-gold-accent"
				/>
			</div>
		</div>

		{#if data.analyticsError}
			<div class="bg-system-error/10 border border-system-error/30 rounded-xl p-4 mb-6 text-system-error text-sm">
				{data.analyticsError}
			</div>
		{/if}

		{#if data.analytics}
			<!-- Date label -->
			<p class="text-muted text-sm mb-4">
				Showing data for <strong class="text-primary">{data.analytics.business_date}</strong>
			</p>

			<!-- Summary tiles -->
			<div class="grid grid-cols-2 sm:grid-cols-4 gap-4 mb-8">
				<Card.Root id="analytics-total-visits" class="rounded-2xl border-white/[0.05] py-0 gap-0 shadow-lg">
					<Card.Content class="p-5">
						<p class="text-xs text-muted mb-1 uppercase tracking-wider">Total Visits</p>
						<p class="text-3xl font-mono font-bold text-primary">{data.analytics.total_visits}</p>
					</Card.Content>
				</Card.Root>
				<Card.Root id="analytics-total-revenue" class="rounded-2xl border-white/[0.05] py-0 gap-0 shadow-lg">
					<Card.Content class="p-5">
						<p class="text-xs text-muted mb-1 uppercase tracking-wider">Total Revenue</p>
						<p class="text-3xl font-mono font-bold text-gold-accent">
							{formatRupees(data.analytics.total_revenue_paise)}
						</p>
					</Card.Content>
				</Card.Root>
				<Card.Root id="analytics-avg-wait" class="rounded-2xl border-white/[0.05] py-0 gap-0 shadow-lg">
					<Card.Content class="p-5">
						<p class="text-xs text-muted mb-1 uppercase tracking-wider">Avg Wait</p>
						<p class="text-3xl font-mono font-bold text-primary">
							{data.analytics.average_wait_minutes ?? 0}<span class="text-base text-muted ml-1"
								>min</span
							>
						</p>
					</Card.Content>
				</Card.Root>
				<Card.Root id="analytics-no-shows" class="rounded-2xl border-white/[0.05] py-0 gap-0 shadow-lg">
					<Card.Content class="p-5">
						<p class="text-xs text-muted mb-1 uppercase tracking-wider">No-shows</p>
						<p class="text-3xl font-mono font-bold text-primary">{data.analytics.no_show_count ?? 0}</p>
					</Card.Content>
				</Card.Root>
			</div>

			<!-- Barber breakdown table -->
			{#if data.analytics.barber_breakdown && data.analytics.barber_breakdown.length > 0}
				<Card.Root class="rounded-2xl border-white/[0.05] py-0 gap-0 overflow-hidden shadow-xl">
					<Card.Header class="border-b border-white/[0.05] py-4">
						<Card.Title class="text-lg">Barber Breakdown</Card.Title>
					</Card.Header>
					<Card.Content class="p-0">
						<Table.Root>
							<Table.Header>
								<Table.Row class="hover:bg-transparent">
									<Table.Head>Barber</Table.Head>
									<Table.Head class="text-right">Visits</Table.Head>
									<Table.Head class="text-right">Revenue</Table.Head>
									<Table.Head class="text-right">Avg Service</Table.Head>
								</Table.Row>
							</Table.Header>
							<Table.Body>
								{#each data.analytics.barber_breakdown as row}
									{@const revenueShare =
										maxBarberRevenue > 0 ? ((row.revenue_paise ?? 0) / maxBarberRevenue) * 100 : 0}
									<Table.Row>
										<Table.Cell class="relative font-medium">
											{#if revenueShare > 0}
												<span
													class="absolute bottom-0 left-0 h-[3px] bg-gold-accent/30"
													style="width: {revenueShare}%"
													aria-hidden="true"
												></span>
											{/if}
											<span class="relative">{row.barber_name ?? '—'}</span>
										</Table.Cell>
										<Table.Cell class="text-right font-mono">{row.visits_completed ?? 0}</Table.Cell>
										<Table.Cell
											id="barber-revenue-cell"
											class="text-right font-mono font-medium text-gold-accent"
											>{formatRupees(row.revenue_paise)}</Table.Cell
										>
										<Table.Cell class="text-right font-mono"
											>{row.average_service_minutes ?? 0} min</Table.Cell
										>
									</Table.Row>
								{/each}
							</Table.Body>
						</Table.Root>
					</Card.Content>
				</Card.Root>
			{:else}
				<div class="bg-matte border border-white/[0.05] rounded-2xl p-8 text-center">
					<p class="text-muted">No barber breakdown available for this date.</p>
				</div>
			{/if}
		{:else if !data.analyticsError}
			<div class="bg-matte border border-white/[0.05] rounded-2xl p-12 text-center">
				<p class="text-muted text-lg">No analytics data for this date.</p>
			</div>
		{/if}
	</div>
</div>
