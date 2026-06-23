<script lang="ts">
	import { goto } from '$app/navigation';
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
</script>

<svelte:head>
	<title>Analytics — Admin — BarberBase</title>
	<meta
		name="description"
		content="Daily revenue, visit counts, and barber performance analytics"
	/>
</svelte:head>

<div class="min-h-screen bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
	<div class="max-w-4xl mx-auto p-6">
		<!-- Header -->
		<div class="flex items-center justify-between mb-6">
			<div class="flex items-center gap-3">
				<a href="/admin" class="text-muted hover:text-white transition-colors text-sm"
					>← Admin</a
				>
				<span class="text-dim">/</span>
				<h1 class="text-2xl font-bold text-white">Analytics</h1>
			</div>
			<!-- Date picker — changing reloads via goto -->
			<div>
				<label for="analytics-date-picker" class="sr-only">Select Date</label>
				<input
					id="analytics-date-picker"
					type="date"
					value={selectedDate}
					onchange={handleDateChange}
					class="bg-slate-700 border border-slate-600 rounded-xl px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500"
				/>
			</div>
		</div>

		{#if data.analyticsError}
			<div class="bg-red-900/30 border border-red-700 rounded-xl p-4 mb-6 text-system-error/80 text-sm">
				{data.analyticsError}
			</div>
		{/if}

		{#if data.analytics}
			<!-- Date label -->
			<p class="text-muted text-sm mb-4">
				Showing data for <strong class="text-white">{data.analytics.business_date}</strong>
			</p>

			<!-- Summary cards -->
			<div class="grid grid-cols-2 sm:grid-cols-4 gap-4 mb-8">
				<div
					id="analytics-total-visits"
					class="bg-slate-800 border border-white/[0.05] rounded-2xl p-5 shadow-lg"
				>
					<p class="text-xs text-muted mb-1 uppercase tracking-wider">Total Visits</p>
					<p class="text-3xl font-bold text-white">{data.analytics.total_visits}</p>
				</div>
				<div
					id="analytics-total-revenue"
					class="bg-slate-800 border border-white/[0.05] rounded-2xl p-5 shadow-lg"
				>
					<p class="text-xs text-muted mb-1 uppercase tracking-wider">Total Revenue</p>
					<p class="text-3xl font-bold text-gold-accent">
						{formatRupees(data.analytics.total_revenue_paise)}
					</p>
				</div>
				<div
					id="analytics-avg-wait"
					class="bg-slate-800 border border-white/[0.05] rounded-2xl p-5 shadow-lg"
				>
					<p class="text-xs text-muted mb-1 uppercase tracking-wider">Avg Wait</p>
					<p class="text-3xl font-bold text-white">
						{data.analytics.average_wait_minutes ?? 0}<span class="text-base text-muted ml-1"
							>min</span
						>
					</p>
				</div>
				<div
					id="analytics-no-shows"
					class="bg-slate-800 border border-white/[0.05] rounded-2xl p-5 shadow-lg"
				>
					<p class="text-xs text-muted mb-1 uppercase tracking-wider">No-shows</p>
					<p class="text-3xl font-bold text-white">{data.analytics.no_show_count ?? 0}</p>
				</div>
			</div>

			<!-- Barber breakdown table -->
			{#if data.analytics.barber_breakdown && data.analytics.barber_breakdown.length > 0}
				<div class="bg-slate-800 border border-white/[0.05] rounded-2xl overflow-hidden shadow-xl">
					<div class="px-6 py-4 border-b border-white/[0.05]">
						<h2 class="text-lg font-bold text-white">Barber Breakdown</h2>
					</div>
					<table class="w-full">
						<thead>
							<tr class="border-b border-white/[0.05]">
								<th
									class="px-6 py-3 text-left text-xs text-muted font-medium uppercase tracking-wider"
									>Barber</th
								>
								<th
									class="px-4 py-3 text-right text-xs text-muted font-medium uppercase tracking-wider"
									>Visits</th
								>
								<th
									class="px-4 py-3 text-right text-xs text-muted font-medium uppercase tracking-wider"
									>Revenue</th
								>
								<th
									class="px-4 py-3 text-right text-xs text-muted font-medium uppercase tracking-wider"
									>Avg Service</th
								>
							</tr>
						</thead>
						<tbody class="divide-y divide-slate-700/50">
							{#each data.analytics.barber_breakdown as row}
								<tr class="hover:bg-slate-700/20 transition-colors">
									<td class="px-6 py-4 text-white font-medium text-sm">{row.barber_name ?? '—'}</td>
									<td class="px-4 py-4 text-primary text-sm text-right"
										>{row.visits_completed ?? 0}</td
									>
									<td
										id="barber-revenue-cell"
										class="px-4 py-4 text-gold-accent text-sm text-right font-mono font-medium"
										>{formatRupees(row.revenue_paise)}</td
									>
									<td class="px-4 py-4 text-primary text-sm text-right"
										>{row.average_service_minutes ?? 0} min</td
									>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{:else}
				<div class="bg-slate-800 border border-white/[0.05] rounded-2xl p-8 text-center">
					<p class="text-muted">No barber breakdown available for this date.</p>
				</div>
			{/if}
		{:else if !data.analyticsError}
			<div class="bg-slate-800 border border-white/[0.05] rounded-2xl p-12 text-center">
				<p class="text-muted text-lg">No analytics data for this date.</p>
			</div>
		{/if}
	</div>
</div>
