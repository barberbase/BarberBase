<script lang="ts">
	import { enhance } from '$app/forms';
	import Icon from '$lib/components/Icon.svelte';
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
		open: 'Open',
		closed: 'Closed',
		temporarily_closed: 'Temporarily Closed',
		closing_soon: 'Closing Soon'
	};

	function statusBadge(s: string) {
		return (
			{
				open: 'bg-system-success/10 text-system-success border-system-success/30',
				closed: 'bg-system-error/10 text-system-error border-system-error/30',
				temporarily_closed: 'bg-system-warning/10 text-system-warning border-system-warning/30',
				closing_soon: 'bg-system-warning/10 text-system-warning border-system-warning/30'
			}[s] || 'bg-surface text-muted border-white/[0.05]'
		);
	}

	function statusDot(s: string) {
		return (
			{
				open: 'bg-system-success',
				closed: 'bg-system-error',
				temporarily_closed: 'bg-system-warning',
				closing_soon: 'bg-system-warning'
			}[s] || 'bg-dim'
		);
	}
</script>

<svelte:head>
	<title>Shop Status — Admin — BarberBase</title>
	<meta name="description" content="Control shop open/close status and manage queue entries" />
</svelte:head>

<div class="min-h-screen bg-canvas">
	<div class="max-w-2xl mx-auto p-6">
		<!-- Header -->
		<div class="flex items-center gap-3 mb-6">
			<a href="/admin" class="text-muted hover:text-primary transition-colors text-sm">← Admin</a>
			<span class="text-dim">/</span>
			<h1 class="text-2xl font-bold text-primary">Shop Status</h1>
		</div>

		{#if form?.error}
			<div class="bg-system-error/10 border border-system-error/30 rounded-xl p-4 mb-6 text-system-error text-sm">
				{form.error}
			</div>
		{/if}
		{#if form?.success}
			<div
				class="bg-system-success/10 border border-system-success/30 rounded-xl p-4 mb-6 text-system-success text-sm flex items-center gap-2"
			>
				<Icon name="check" size={14} /> Shop status updated
			</div>
		{/if}

		<!-- Current Status Card -->
		{#if data.shopStatus}
			<div class="bg-matte border border-white/[0.05] rounded-2xl p-6 mb-6 shadow-xl">
				<div class="flex items-center justify-between mb-4">
					<h2 class="text-lg font-bold text-primary">Current Status</h2>
					<span
						class="px-3 py-1 rounded-full border text-sm font-medium inline-flex items-center gap-2 {statusBadge(
							data.shopStatus.shop_status
						)}"
					>
						<span class="inline-block w-1.5 h-1.5 rounded-full {statusDot(data.shopStatus.shop_status)}"></span>
						{statusLabel[data.shopStatus.shop_status] ?? data.shopStatus.shop_status}
					</span>
				</div>
				<div class="grid grid-cols-2 gap-4 text-sm">
					<div>
						<p class="text-muted text-xs mb-1">Manual Override</p>
						<p class="text-primary">
							{data.shopStatus.manual_override_active ? 'Active' : 'None'}
						</p>
					</div>
					{#if data.shopStatus.override_expires_at}
						<div>
							<p class="text-muted text-xs mb-1">Expires At</p>
							<p class="text-primary text-xs">
								{new Date(data.shopStatus.override_expires_at).toLocaleString('en-IN')}
							</p>
						</div>
					{/if}
				</div>

				<!-- Counter PIN -->
				{#if data.shopStatus.arrival_pin}
					<div class="mt-4 p-4 bg-gold-accent/10 border border-gold-accent/30 rounded-xl">
						<p class="text-xs text-gold-accent font-semibold mb-1">
							Counter PIN — show this to customers for arrival verification
						</p>
						<p
							id="arrival-pin-display"
							class="text-4xl font-bold font-mono text-gold-accent tracking-widest"
						>
							{data.shopStatus.arrival_pin}
						</p>
						<form method="POST" action="?/regeneratePin" use:enhance class="mt-3">
							<button
								type="submit"
								class="text-xs text-gold-accent hover:text-primary transition-colors inline-flex items-center gap-1.5"
							>
								<Icon name="refresh" size={12} /> Regenerate PIN
							</button>
						</form>
					</div>
				{/if}
				{#if form?.pin_success && form?.new_pin}
					<div class="mt-3 p-3 bg-system-success/10 border border-system-success/30 rounded-xl">
						<p class="text-xs text-system-success">New PIN generated:</p>
						<p class="text-2xl font-bold font-mono text-system-success tracking-widest">
							{form.new_pin}
						</p>
					</div>
				{/if}
			</div>
		{/if}

		<!-- Change Status Form -->
		<div class="bg-matte border border-white/[0.05] rounded-2xl p-6 shadow-xl">
			<h2 class="text-lg font-bold text-primary mb-4">Change Status</h2>
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
								class="border rounded-xl p-3 text-center text-sm font-medium transition-all duration-150 border-white/[0.05] text-dim peer-checked:border-gold-accent peer-checked:text-gold-accent peer-checked:bg-gold-accent/10 hover:border-white/20 hover:text-primary flex items-center justify-center gap-2"
							>
								<span class="inline-block w-1.5 h-1.5 rounded-full {statusDot(s)}"></span>
								{#if s === 'open'}Open{:else if s === 'closed'}Closed{:else}Temp Closed{/if}
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
							class="w-full bg-titanium border border-white/[0.05] rounded-lg px-3 py-2 text-primary text-sm focus:outline-none focus:ring-2 focus:ring-gold-accent"
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
					class="w-full bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:brightness-100 text-canvas font-bold py-3 rounded-xl transition-all duration-150"
				>
					Update Status
				</button>
			</form>
		</div>
	</div>
</div>

<!-- 422 Modal: Active customers conflict -->
{#if showModal}
	<div class="fixed inset-0 bg-canvas/85 backdrop-blur-sm flex items-center justify-center z-50 p-4">
		<div
			id="shop-status-conflict-modal"
			class="bg-matte border border-white/[0.05] rounded-2xl p-6 max-w-md w-full shadow-2xl machined-edge"
		>
			<h3 class="text-xl font-bold text-primary mb-2 flex items-center gap-2">
				<span class="text-system-warning"><Icon name="alert" size={20} /></span>
				Active Customers Waiting
			</h3>
			<p class="text-primary mb-6 text-sm">
				There {activeEntryCount === 1 ? 'is' : 'are'}
				<strong class="text-primary">{activeEntryCount}</strong>
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
						class="w-full bg-primary hover:brightness-110 active:brightness-90 active:scale-[0.98] text-canvas font-bold py-3 rounded-xl transition-all duration-150 text-sm"
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
						class="w-full bg-system-error/10 border border-system-error/30 hover:bg-system-error hover:text-canvas active:scale-[0.98] text-system-error font-bold py-3 rounded-xl transition-all duration-150 text-sm"
					>
						Cancel all waiting customers
					</button>
				</form>
			</div>
		</div>
	</div>
{/if}
