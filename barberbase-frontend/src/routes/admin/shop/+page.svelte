<script lang="ts">
	import { enhance } from '$app/forms';
	import type { PageData, ActionData } from './$types';

	let { data, form }: { data: PageData; form: ActionData } = $props();

	let selectedStatus = $state<string>('');
	let expiresInMinutes = $state<string>('');

	// Modal state — shown on 422 from server
	let showModal = $derived(!!(form as any)?.needs_modal);
	let pendingStatus = $derived((form as any)?.pending_status ?? '');
	let pendingExpires = $derived((form as any)?.pending_expires ?? null);
	let activeEntryCount = $derived((form as any)?.active_entry_count ?? 0);

	const statusLabel: Record<string, string> = {
		open: '🟢 Open',
		closed: '🔴 Closed',
		temporarily_closed: '⏸ Temporarily Closed',
		closing_soon: '🟡 Closing Soon'
	};

	function statusBadge(s: string) {
		return (
			{
				open: 'bg-green-900/40 text-green-400 border-green-700',
				closed: 'bg-red-900/40 text-system-error/80 border-red-700',
				temporarily_closed: 'bg-yellow-900/40 text-yellow-400 border-yellow-700',
				closing_soon: 'bg-orange-900/40 text-orange-400 border-orange-700'
			}[s] || 'bg-slate-700 text-muted border-slate-600'
		);
	}
</script>

<svelte:head>
	<title>Shop Status — Admin — BarberBase</title>
	<meta name="description" content="Control shop open/close status and manage queue entries" />
</svelte:head>

<div class="min-h-screen bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
	<div class="max-w-2xl mx-auto p-6">
		<!-- Header -->
		<div class="flex items-center gap-3 mb-6">
			<a href="/admin" class="text-muted hover:text-white transition-colors text-sm">← Admin</a>
			<span class="text-dim">/</span>
			<h1 class="text-2xl font-bold text-white">Shop Status</h1>
		</div>

		{#if form?.error}
			<div class="bg-red-900/30 border border-red-700 rounded-xl p-4 mb-6 text-system-error/80 text-sm">
				{form.error}
			</div>
		{/if}
		{#if form?.success}
			<div
				class="bg-green-900/30 border border-green-700 rounded-xl p-4 mb-6 text-green-400 text-sm"
			>
				✓ Shop status updated
			</div>
		{/if}

		<!-- Current Status Card -->
		{#if data.shopStatus}
			<div class="bg-slate-800 border border-white/[0.05] rounded-2xl p-6 mb-6 shadow-xl">
				<div class="flex items-center justify-between mb-4">
					<h2 class="text-lg font-bold text-white">Current Status</h2>
					<span
						class="px-3 py-1 rounded-full border text-sm font-medium {statusBadge(
							data.shopStatus.shop_status
						)}"
					>
						{statusLabel[data.shopStatus.shop_status] ?? data.shopStatus.shop_status}
					</span>
				</div>
				<div class="grid grid-cols-2 gap-4 text-sm">
					<div>
						<p class="text-muted text-xs mb-1">Manual Override</p>
						<p class="text-white">
							{data.shopStatus.manual_override_active ? '✓ Active' : '— None'}
						</p>
					</div>
					{#if data.shopStatus.override_expires_at}
						<div>
							<p class="text-muted text-xs mb-1">Expires At</p>
							<p class="text-white text-xs">
								{new Date(data.shopStatus.override_expires_at).toLocaleString('en-IN')}
							</p>
						</div>
					{/if}
				</div>

				<!-- Counter PIN -->
				{#if data.shopStatus.arrival_pin}
					<div class="mt-4 p-4 bg-gold-accent/10/20 border border-amber-700/50 rounded-xl">
						<p class="text-xs text-gold-accent font-semibold mb-1">
							Counter PIN — show this to customers for arrival verification
						</p>
						<p
							id="arrival-pin-display"
							class="text-4xl font-bold font-mono text-amber-300 tracking-widest"
						>
							{data.shopStatus.arrival_pin}
						</p>
						<form method="POST" action="?/regeneratePin" use:enhance class="mt-3">
							<button
								type="submit"
								class="text-xs text-gold-accent hover:text-gold-accent/80 transition-colors"
							>
								↻ Regenerate PIN
							</button>
						</form>
					</div>
				{/if}
				{#if form?.pin_success && form?.new_pin}
					<div class="mt-3 p-3 bg-green-900/20 border border-green-700 rounded-xl">
						<p class="text-xs text-green-400">New PIN generated:</p>
						<p class="text-2xl font-bold font-mono text-green-300 tracking-widest">
							{form.new_pin}
						</p>
					</div>
				{/if}
			</div>
		{/if}

		<!-- Change Status Form -->
		<div class="bg-slate-800 border border-white/[0.05] rounded-2xl p-6 shadow-xl">
			<h2 class="text-lg font-bold text-white mb-4">Change Status</h2>
			<form id="set-shop-status-form" method="POST" action="?/setStatus" use:enhance>
				<div class="grid grid-cols-3 gap-3 mb-4">
					{#each ['open', 'closed', 'temporarily_closed'] as s}
						<label class="cursor-pointer">
							<input
								type="radio"
								name="status"
								value={s}
								bind:group={selectedStatus}
								class="sr-only peer"
							/>
							<div
								class="border rounded-xl p-3 text-center text-sm font-medium transition-all border-slate-600 text-gold-accent/80/60 peer-checked:border-gold-accent peer-checked:text-gold-accent peer-checked:bg-gold-accent/10/20 hover:border-slate-500 hover:text-white"
							>
								{#if s === 'open'}🟢 Open{:else if s === 'closed'}🔴 Closed{:else}⏸ Temp Closed{/if}
							</div>
						</label>
					{/each}
				</div>

				<!-- Expires selector — only shown for temporarily_closed -->
				{#if selectedStatus === 'temporarily_closed'}
					<div class="mb-4">
						<label for="expires-select" class="block text-xs text-muted mb-2"
							>Close for how long?</label
						>
						<select
							id="expires-select"
							name="expires_in_minutes"
							bind:value={expiresInMinutes}
							class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500"
						>
							<option value="15">15 minutes</option>
							<option value="30">30 minutes</option>
							<option value="60">60 minutes</option>
							<option value="">Until I reopen manually</option>
						</select>
					</div>
				{/if}

				<button
					id="submit-shop-status-btn"
					type="submit"
					disabled={!selectedStatus}
					class="w-full bg-gold-accent hover:bg-amber-400 disabled:opacity-40 disabled:cursor-not-allowed text-canvas font-bold py-3 rounded-xl transition-all"
				>
					Update Status
				</button>
			</form>
		</div>
	</div>
</div>

<!-- 422 Modal: Active customers conflict -->
{#if showModal}
	<div class="fixed inset-0 bg-black/70 backdrop-blur-sm flex items-center justify-center z-50 p-4">
		<div
			id="shop-status-conflict-modal"
			class="bg-slate-800 border border-white/[0.05] rounded-2xl p-6 max-w-md w-full shadow-2xl"
		>
			<h3 class="text-xl font-bold text-white mb-2">⚠️ Active Customers Waiting</h3>
			<p class="text-primary mb-6 text-sm">
				There {activeEntryCount === 1 ? 'is' : 'are'}
				<strong class="text-white">{activeEntryCount}</strong>
				customer{activeEntryCount !== 1 ? 's' : ''} waiting. What would you like to do?
			</p>
			<div class="grid grid-cols-1 gap-3">
				<!-- Serve them first -->
				<form method="POST" action="?/setStatus" use:enhance>
					<input type="hidden" name="status" value={pendingStatus} />
					{#if pendingExpires !== null}<input
							type="hidden"
							name="expires_in_minutes"
							value={pendingExpires}
						/>{/if}
					<input type="hidden" name="modal_action" value="finish_remaining" />
					<button
						id="modal-finish-remaining-btn"
						type="submit"
						class="w-full bg-blue-600 hover:bg-blue-500 text-white font-bold py-3 rounded-xl transition-all text-sm"
					>
						Serve them first, then close
					</button>
				</form>
				<!-- Cancel all -->
				<form method="POST" action="?/setStatus" use:enhance>
					<input type="hidden" name="status" value={pendingStatus} />
					{#if pendingExpires !== null}<input
							type="hidden"
							name="expires_in_minutes"
							value={pendingExpires}
						/>{/if}
					<input type="hidden" name="modal_action" value="expire_remaining" />
					<button
						id="modal-expire-remaining-btn"
						type="submit"
						class="w-full bg-red-700 hover:bg-red-600 text-white font-bold py-3 rounded-xl transition-all text-sm"
					>
						Cancel all waiting customers
					</button>
				</form>
			</div>
		</div>
	</div>
{/if}
