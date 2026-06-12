<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { QueueStore } from '$lib/stores/queue.svelte';
	import { connectSSE } from '$lib/sse';
	import CheckoutModal from '$lib/components/CheckoutModal.svelte';

	// SvelteKit SSR data
	let { data } = $props<{
		data: {
			snapshot: any;
			locationId: string;
			accessToken: string;
			staffMembers: any[];
			catalog: any;
		};
	}>();

	// Initialize the store using a snapshot to avoid Svelte 5 local reference warnings
	const initialData = $state.snapshot(data);
	const store = new QueueStore(initialData.accessToken, initialData.snapshot, initialData.apiBase);

	// Connect SSE on mount
	let sseClient: ReturnType<typeof connectSSE> | null = null;
	onMount(() => {
		sseClient = connectSSE(
			initialData.locationId,
			initialData.accessToken,
			store,
			initialData.apiBase
		);
	});

	onDestroy(() => {
		if (sseClient) {
			sseClient.close();
		}
	});

	// UI States
	let showWalkInForm = $state<boolean>(false);
	let selectedEntryForCheckout = $state<any | null>(null);
	let activeActions = $state<Record<string, boolean>>({});
	const pendingActions = new Set<string>();

	// Walk-in form inputs
	let walkInName = $state<string>('');
	let walkInPhone = $state<string>('');
	let walkInPartySize = $state<number>(1);
	let walkInBarberId = $state<string>('');
	let walkInSelectedVariants = $state<string[]>([]);
	let walkInError = $state<string>('');

	// Helper to track and debounce staff actions (disable for 1s)
	function runDebouncedAction(actionKey: string, fn: () => Promise<unknown>) {
		if (pendingActions.has(actionKey)) return;
		pendingActions.add(actionKey);
		activeActions[actionKey] = true;

		fn().finally(() => {
			setTimeout(() => {
				pendingActions.delete(actionKey);
				activeActions[actionKey] = false;
			}, 1000);
		});
	}

	// Format paise to INR representation
	function formatCurrency(paise: number): string {
		return `₹${(paise / 100).toFixed(2)}`;
	}

	// Flatten catalog variants
	const allVariants = $derived(() => {
		if (!data.catalog || !data.catalog.categories) return [];
		const list: any[] = [];
		for (const cat of data.catalog.categories) {
			for (const grp of cat.groups) {
				for (const vr of grp.variants) {
					list.push({
						id: vr.id,
						name: vr.name,
						groupName: grp.name,
						categoryName: cat.name,
						price: vr.price_paise,
						duration: vr.duration_minutes
					});
				}
			}
		}
		return list;
	});

	// Sorted queue entries: in_progress -> called -> waiting -> skipped -> others
	const sortedEntries = $derived(() => {
		if (!store.snapshot || !store.snapshot.entries) return [];

		const inProgress: any[] = [];
		const called: any[] = [];
		const waiting: any[] = [];
		const skipped: any[] = [];
		const others: any[] = [];

		for (const entry of store.snapshot.entries) {
			if (entry.state === 'in_progress') {
				inProgress.push(entry);
			} else if (entry.state === 'called') {
				called.push(entry);
			} else if (entry.state === 'waiting') {
				waiting.push(entry);
			} else if (entry.state === 'skipped') {
				skipped.push(entry);
			} else {
				others.push(entry);
			}
		}

		// Sort waiting list by priority_group ASC, sort_key ASC, fallback to joined_at ASC
		waiting.sort((a, b) => {
			const pgA = typeof a.priority_group === 'number' ? a.priority_group : 100;
			const pgB = typeof b.priority_group === 'number' ? b.priority_group : 100;
			if (pgA !== pgB) return pgA - pgB;

			const skA =
				typeof a.sort_key === 'number' ? a.sort_key : a.sort_key ? parseInt(a.sort_key) : null;
			const skB =
				typeof b.sort_key === 'number' ? b.sort_key : b.sort_key ? parseInt(b.sort_key) : null;
			if (skA !== null && skB !== null && skA !== skB) return skA - skB;

			return new Date(a.joined_at).getTime() - new Date(b.joined_at).getTime();
		});

		// Sort skipped list by joined_at ASC
		skipped.sort((a, b) => new Date(a.joined_at).getTime() - new Date(b.joined_at).getTime());

		return [...inProgress, ...called, ...waiting, ...skipped, ...others];
	});

	// Active counts
	const activeCount = $derived(
		store.snapshot?.entries?.filter(
			(e) => e.state === 'waiting' || e.state === 'called' || e.state === 'in_progress'
		).length || 0
	);

	// E.164 phone auto-correction helper
	function preparePhoneNumber(raw: string): string {
		let clean = raw.trim();
		if (!clean) return '';
		if (clean.length === 10 && /^\d+$/.test(clean)) {
			return `+91${clean}`;
		}
		return clean;
	}

	async function handleAddWalkIn(e: Event) {
		e.preventDefault();
		walkInError = '';

		if (walkInSelectedVariants.length === 0) {
			walkInError = 'Please select at least one service variant.';
			return;
		}

		let phone: string | undefined = undefined;
		if (walkInPhone) {
			phone = preparePhoneNumber(walkInPhone);
			const e164Pattern = /^\+[1-9]\d{1,14}$/;
			if (!e164Pattern.test(phone)) {
				walkInError = 'Phone number must be in E.164 format (e.g. +919876543210).';
				return;
			}
		}

		try {
			await store.addWalkIn({
				variant_ids: walkInSelectedVariants,
				customer_name: walkInName.trim() || undefined,
				phone_number: phone,
				party_size: walkInPartySize,
				requested_barber_id: walkInBarberId || undefined
			});

			// Reset form
			walkInName = '';
			walkInPhone = '';
			walkInPartySize = 1;
			walkInBarberId = '';
			walkInSelectedVariants = [];
			showWalkInForm = false;
		} catch (err: any) {
			console.error(err);
			walkInError = err?.data?.message || 'Failed to add walk-in customer.';
		}
	}
</script>

<svelte:head>
	<title>Staff Queue Dashboard — BarberBase</title>
</svelte:head>

<div class="min-h-screen bg-slate-950 text-slate-100 flex flex-col font-sans">
	<!-- Top Navigation Header -->
	<header
		class="bg-slate-900 border-b border-slate-800 px-6 py-4 flex flex-wrap justify-between items-center gap-4"
	>
		<div class="flex items-center space-x-3">
			<span class="text-xl font-extrabold text-amber-500 tracking-wider">BarberBase</span>
			<span class="text-slate-500">|</span>
			<span class="text-sm font-semibold text-slate-300">Staff Dashboard</span>
		</div>

		<!-- Status Indicators -->
		<div class="flex items-center space-x-4">
			<!-- SSE Live Sync indicator -->
			<div
				class="flex items-center space-x-1.5 text-xs bg-slate-950 border border-slate-800 rounded-full px-3 py-1 font-semibold"
			>
				<span class="relative flex h-2.5 w-2.5">
					<span
						class="animate-ping absolute inline-flex h-full w-full rounded-full opacity-75 {store.sseConnected
							? 'bg-emerald-400'
							: 'bg-rose-400'}"
					></span>
					<span
						class="relative inline-flex rounded-full h-2.5 w-2.5 {store.sseConnected
							? 'bg-emerald-500'
							: 'bg-rose-500'}"
					></span>
				</span>
				<span class={store.sseConnected ? 'text-emerald-400' : 'text-rose-400'}>
					{store.sseConnected ? 'Live' : 'SSE Offline'}
				</span>
			</div>

			<!-- Queue Session Status Badge -->
			{#if store.snapshot}
				<div
					class="text-xs font-bold uppercase tracking-wider px-3 py-1 rounded-full border border-slate-700 bg-slate-850 flex items-center"
				>
					Session:
					<span
						class="ml-1.5 {store.snapshot.session_status === 'active'
							? 'text-emerald-400'
							: 'text-amber-500'}"
					>
						{store.snapshot.session_status}
					</span>
				</div>
			{/if}

			<!-- Barber Name -->
			<div class="text-sm text-slate-300">
				Hello, <span class="font-bold text-white">{data.snapshot ? 'Barber' : 'Staff'}</span>
			</div>
		</div>
	</header>

	<!-- Operational Alert Banners -->
	{#if store.snapshot && (store.snapshot.session_status === 'closed' || store.snapshot.session_status === 'ending')}
		<div
			class="bg-amber-950/40 border-b border-amber-900/40 px-6 py-3.5 text-sm text-amber-300 flex items-center space-x-3"
		>
			<svg
				xmlns="http://www.w3.org/2000/svg"
				class="h-5 w-5 shrink-0"
				fill="none"
				viewBox="0 0 24 24"
				stroke="currentColor"
			>
				<path
					stroke-linecap="round"
					stroke-linejoin="round"
					stroke-width="2"
					d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
				/>
			</svg>
			<div>
				<strong>Attention:</strong> The queue session is currently in
				<span class="font-extrabold uppercase">{store.snapshot.session_status}</span>
				status.
				{#if store.snapshot.session_status === 'ending'}
					New online registrations are blocked; please serve the remaining customers.
				{:else}
					The queue session is closed for the day. No further check-ins or dispatches will process.
				{/if}
			</div>
		</div>
	{/if}

	<main class="flex-1 max-w-7xl w-full mx-auto p-6 flex flex-col lg:flex-row gap-6">
		<!-- Left Side: Queue Controls and Add Walk-in -->
		<section class="w-full lg:w-1/3 flex flex-col space-y-6">
			<!-- Primary Dispatch Console -->
			<div class="bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-lg">
				<h2 class="text-lg font-bold text-slate-100 mb-4 tracking-wide">Queue Controller</h2>

				<div class="space-y-4">
					<!-- BIG Call Next Button -->
					<button
						type="button"
						class="w-full py-5 bg-amber-500 hover:bg-amber-400 active:bg-amber-600 disabled:opacity-40 disabled:hover:bg-amber-500 text-slate-950 font-black text-xl rounded-2xl transition-all duration-150 shadow-lg cursor-pointer flex flex-col items-center justify-center space-y-1"
						disabled={activeActions['call-next'] || store.snapshot?.session_status === 'closed'}
						onclick={() =>
							runDebouncedAction('call-next', () =>
								store
									.callNext()
									.catch((err) => alert(err?.data?.message || 'Failed to call next customer.'))
							)}
					>
						<span>🎙️ CALL NEXT CLIENT</span>
						<span class="text-xs font-semibold opacity-85">Single-Tap Dispatch</span>
					</button>

					<!-- Total wait & count summary stats -->
					<div class="grid grid-cols-2 gap-3 pt-2">
						<div class="bg-slate-950 border border-slate-800 rounded-xl p-3.5 text-center">
							<div class="text-xs font-medium text-slate-400">Total Active</div>
							<div class="text-2xl font-black text-slate-200 mt-1">{activeCount}</div>
						</div>
						<div class="bg-slate-950 border border-slate-800 rounded-xl p-3.5 text-center">
							<div class="text-xs font-medium text-slate-400">Version</div>
							<div class="text-2xl font-black text-slate-200 mt-1">{store.localQueueVersion}</div>
						</div>
					</div>
				</div>
			</div>

			<!-- Add Walk-in Console Panel -->
			<div class="bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-lg">
				<div class="flex justify-between items-center">
					<h2 class="text-lg font-bold text-slate-100 tracking-wide">Add Walk-in Client</h2>
					<button
						type="button"
						class="px-3 py-1.5 text-xs font-bold rounded-xl border border-slate-700 hover:bg-slate-800 transition-colors"
						onclick={() => {
							showWalkInForm = !showWalkInForm;
						}}
					>
						{showWalkInForm ? 'Collapse Form' : 'Expand Form'}
					</button>
				</div>

				{#if showWalkInForm}
					<form
						onsubmit={handleAddWalkIn}
						class="space-y-4 pt-4 border-t border-slate-800 mt-4 transition-all duration-200"
					>
						<!-- Name -->
						<div>
							<label for="walk-in-name" class="block text-xs font-medium text-slate-400 mb-1"
								>Customer Name (Optional)</label
							>
							<input
								type="text"
								id="walk-in-name"
								placeholder="e.g. Rahul, Guest, Uncle"
								maxlength="80"
								class="w-full bg-slate-950 border border-slate-800 rounded-xl px-3 py-2 text-sm text-slate-100 focus:outline-none focus:border-amber-500 placeholder:text-slate-650"
								bind:value={walkInName}
							/>
						</div>

						<!-- Phone -->
						<div>
							<label for="walk-in-phone" class="block text-xs font-medium text-slate-400 mb-1"
								>Phone Number (Optional)</label
							>
							<input
								type="tel"
								id="walk-in-phone"
								placeholder="e.g. 9876543210"
								class="w-full bg-slate-950 border border-slate-800 rounded-xl px-3 py-2 text-sm text-slate-100 focus:outline-none focus:border-amber-500 placeholder:text-slate-650"
								bind:value={walkInPhone}
							/>
							<span class="text-[10px] text-slate-500 mt-0.5 block"
								>10-digit number will automatically prefix with +91.</span
							>
						</div>

						<div class="grid grid-cols-2 gap-3">
							<!-- Party Size -->
							<div>
								<label for="party-size" class="block text-xs font-medium text-slate-400 mb-1"
									>Party Size</label
								>
								<input
									type="number"
									id="party-size"
									min="1"
									max="10"
									class="w-full bg-slate-950 border border-slate-800 rounded-xl px-3 py-2 text-sm text-slate-100 focus:outline-none focus:border-amber-500"
									bind:value={walkInPartySize}
								/>
							</div>

							<!-- Barber -->
							<div>
								<label for="walk-in-barber" class="block text-xs font-medium text-slate-400 mb-1"
									>Assigned Barber</label
								>
								<select
									id="walk-in-barber"
									class="w-full bg-slate-950 border border-slate-800 rounded-xl px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-amber-500"
									bind:value={walkInBarberId}
								>
									<option value="">-- Auto Route --</option>
									{#each data.staffMembers as member}
										<option value={member.id}>{member.name}</option>
									{/each}
								</select>
							</div>
						</div>

						<!-- Services (Variant Checklist) -->
						<div class="space-y-1">
							<span class="block text-xs font-medium text-slate-400 mb-1.5"
								>Select Service Variants (Required)</span
							>
							<div
								class="max-h-48 overflow-y-auto bg-slate-950 border border-slate-800 rounded-xl p-3 space-y-2 divide-y divide-slate-800/40"
							>
								{#each allVariants() as v, idx}
									<label class="flex items-start space-x-3 pt-2 first:pt-0 cursor-pointer">
										<input
											type="checkbox"
											value={v.id}
											class="mt-1 rounded text-amber-500 bg-slate-900 border-slate-800 focus:ring-offset-slate-900"
											bind:group={walkInSelectedVariants}
										/>
										<div class="text-xs">
											<div class="font-bold text-slate-200">{v.name}</div>
											<div class="text-[10px] text-slate-500">
												{v.categoryName} • {v.duration} min • {formatCurrency(v.price)}
											</div>
										</div>
									</label>
								{/each}
							</div>
						</div>

						<!-- Error Display -->
						{#if walkInError}
							<div
								class="bg-red-950/40 border border-red-900/50 rounded-xl p-3 text-xs text-red-400 flex items-start space-x-2"
							>
								<svg
									xmlns="http://www.w3.org/2000/svg"
									class="h-4 w-4 shrink-0 mt-0.5"
									fill="none"
									viewBox="0 0 24 24"
									stroke="currentColor"
								>
									<path
										stroke-linecap="round"
										stroke-linejoin="round"
										stroke-width="2"
										d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
									/>
								</svg>
								<div>{walkInError}</div>
							</div>
						{/if}

						<button
							type="submit"
							class="w-full bg-amber-500 hover:bg-amber-400 active:bg-amber-600 text-slate-950 font-bold py-2.5 rounded-xl transition-all duration-150 text-sm cursor-pointer"
						>
							Add Walk-in client
						</button>
					</form>
				{/if}
			</div>
		</section>

		<!-- Right Side: Live Queue Entries -->
		<section class="w-full lg:w-2/3 flex flex-col space-y-4">
			<div class="flex justify-between items-center">
				<h2 class="text-lg font-bold text-slate-200 tracking-wide">Live Queue</h2>
				<span class="text-xs text-slate-400 font-semibold"
					>{sortedEntries().length} active items</span
				>
			</div>

			{#if sortedEntries().length === 0}
				<div
					class="bg-slate-900 border border-slate-800 rounded-2xl p-12 text-center text-slate-450 flex flex-col items-center justify-center space-y-3"
				>
					<svg
						xmlns="http://www.w3.org/2000/svg"
						class="h-12 w-12 text-slate-600"
						fill="none"
						viewBox="0 0 24 24"
						stroke="currentColor"
					>
						<path
							stroke-linecap="round"
							stroke-linejoin="round"
							stroke-width="2"
							d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z"
						/>
					</svg>
					<span class="text-sm font-semibold text-slate-400">The queue is currently empty.</span>
					<span class="text-xs text-slate-500"
						>Tap "Call Next" or add a Walk-in to get started.</span
					>
				</div>
			{:else}
				<div class="space-y-4">
					{#each sortedEntries() as entry (entry.id)}
						<!-- Queue Entry Card with border styling for stale warnings -->
						<div
							class="border rounded-2xl p-4 shadow-md transition-all duration-300 bg-slate-900 border-slate-800 hover:border-slate-700 flex flex-col md:flex-row justify-between gap-4
							{entry.stale_warning === 'called_warning' || entry.stale_warning === 'in_progress_warning'
								? 'border-amber-500 bg-amber-950/20 ring-1 ring-amber-500/30'
								: ''}
							{entry.stale_warning === 'called_critical' || entry.stale_warning === 'in_progress_critical'
								? 'border-red-500 bg-red-950/20 ring-2 ring-red-500/50 animate-pulse-slow'
								: ''}"
						>
							<!-- Left Column: Token + Customer Details -->
							<div class="flex-1 space-y-3">
								<div class="flex items-center space-x-2 flex-wrap gap-y-1">
									<!-- Token Badge -->
									<span
										class="bg-slate-950 border border-slate-800 text-amber-500 text-sm font-extrabold px-3 py-1 rounded-xl"
									>
										#{entry.token_number}
									</span>

									<!-- State Status Badge -->
									<span
										class="text-[10px] font-extrabold uppercase px-2 py-0.5 rounded-md border
										{entry.state === 'in_progress' ? 'bg-emerald-950/30 border-emerald-800 text-emerald-400' : ''}
										{entry.state === 'called' ? 'bg-amber-950/30 border-amber-800 text-amber-400 animate-pulse' : ''}
										{entry.state === 'waiting' ? 'bg-blue-950/30 border-blue-800 text-blue-400' : ''}
										{entry.state === 'skipped' ? 'bg-slate-950/50 border-slate-800 text-slate-400' : ''}"
									>
										{entry.state}
									</span>

									<!-- Presence Badge -->
									<span
										class="text-xs text-slate-300 font-medium bg-slate-950 border border-slate-800/80 rounded-lg px-2 py-0.5"
									>
										{#if entry.presence_state === 'remote'}
											🌐 Remote
										{:else}
											{#if entry.presence_state === 'notified'}
												📨 Notified
											{:else}
												{#if entry.presence_state === 'on_the_way'}
													🏃 On the Way
												{:else}
													{#if entry.presence_state === 'arrived'}
														✅ Arrived
													{:else}
														{#if entry.presence_state === 'snoozed'}
															⏸ Snoozed
														{:else}
															— Walk-in
														{/if}
													{/if}
												{/if}
											{/if}
										{/if}
									</span>

									<!-- Stale Warning urgency banner -->
									{#if entry.stale_warning === 'called_critical' || entry.stale_warning === 'in_progress_critical'}
										<span
											class="text-[10px] font-bold px-2 py-0.5 rounded bg-red-600 text-white animate-pulse"
										>
											⚠️ DELAYED
										</span>
									{/if}
								</div>

								<!-- Customer profile details -->
								<div>
									<div class="font-extrabold text-base text-slate-100">
										{entry.customer?.name || 'Walk-in Customer'}
									</div>
									<div class="flex items-center space-x-2 text-xs text-slate-400 mt-1">
										{#if entry.customer?.phone_masked}
											<span>{entry.customer.phone_masked}</span>
											<span>•</span>
										{/if}
										<span>{entry.customer?.visit_count || 0} visits</span>
									</div>
								</div>

								<!-- Customer Preferences / Notes -->
								{#if entry.customer?.notes && entry.customer.notes.length > 0}
									<div
										class="bg-slate-950/40 border border-slate-800/40 rounded-xl p-2.5 text-xs text-slate-300 space-y-1"
									>
										<span
											class="font-bold text-amber-500 text-[10px] uppercase tracking-wider block"
											>Staff Notes:</span
										>
										<ul class="list-disc pl-4 space-y-0.5">
											{#each entry.customer.notes as note}
												<li>{note}</li>
											{/each}
										</ul>
									</div>
								{/if}

								<!-- Rendered Services list -->
								<div class="flex flex-wrap gap-2 pt-1">
									{#each entry.services as svc}
										<span
											class="text-[10px] bg-slate-950 border border-slate-850 rounded-lg px-2.5 py-1 text-slate-300"
										>
											{svc.name} ({svc.duration_minutes}m)
										</span>
									{/each}
								</div>
							</div>

							<!-- Right Column: Reassignment + Actions Panel -->
							<div
								class="flex flex-col justify-between items-end gap-3 min-w-[200px] w-full md:w-auto"
							>
								<!-- Barber Routing Selector (reassign) -->
								<div class="w-full text-right">
									<label
										for="barber-select-{entry.id}"
										class="block text-[10px] font-medium text-slate-500 mb-1">Assigned Barber</label
									>
									<select
										id="barber-select-{entry.id}"
										class="bg-slate-950 border border-slate-850 rounded-lg px-2 py-1.5 text-xs text-slate-200 focus:outline-none focus:border-amber-500 w-full"
										value={entry.assigned_barber_id || ''}
										onchange={(e) => {
											const barberId = e.currentTarget.value;
											if (barberId) {
												runDebouncedAction(`${entry.id}-reassign`, () =>
													store
														.reassignBarber(entry.id, barberId)
														.catch((err) => alert(err?.data?.message || 'Failed to reassign.'))
												);
											}
										}}
									>
										<option value="">-- Unassigned --</option>
										{#each data.staffMembers as member}
											<option value={member.id}>{member.name}</option>
										{/each}
									</select>
								</div>

								<!-- Action Buttons based on state and presence -->
								<div class="flex flex-wrap gap-2 justify-end w-full">
									{#if entry.state === 'waiting'}
										{#if entry.presence_state === 'arrived'}
											<!-- waiting + presence=arrived -->
											<button
												type="button"
												class="px-4 py-2 bg-emerald-500 hover:bg-emerald-450 active:bg-emerald-600 text-slate-950 font-bold text-xs rounded-xl cursor-pointer transition-colors"
												disabled={activeActions[`${entry.id}-start`]}
												onclick={() =>
													runDebouncedAction(`${entry.id}-start`, () =>
														store
															.startService(entry.id)
															.catch((err) =>
																alert(err?.data?.message || 'Failed to start service.')
															)
													)}
											>
												Direct Start
											</button>
											<button
												type="button"
												class="px-4 py-2 bg-slate-800 hover:bg-slate-700 active:bg-slate-650 text-slate-300 font-bold text-xs rounded-xl cursor-pointer transition-colors"
												disabled={activeActions[`${entry.id}-skip`]}
												onclick={() =>
													runDebouncedAction(`${entry.id}-skip`, () =>
														store
															.skipEntry(entry.id)
															.catch((err) => alert(err?.data?.message || 'Failed to skip entry.'))
													)}
											>
												Skip
											</button>
										{:else}
											<!-- waiting + presence≠arrived -->
											<button
												type="button"
												class="px-4 py-2 bg-slate-800 hover:bg-slate-700 active:bg-slate-650 text-slate-300 font-bold text-xs rounded-xl cursor-pointer transition-colors"
												disabled={activeActions[`${entry.id}-skip`]}
												onclick={() =>
													runDebouncedAction(`${entry.id}-skip`, () =>
														store
															.skipEntry(entry.id)
															.catch((err) => alert(err?.data?.message || 'Failed to skip entry.'))
													)}
											>
												Skip
											</button>
											<button
												type="button"
												class="px-4 py-2 bg-amber-500 hover:bg-amber-450 active:bg-amber-600 text-slate-950 font-bold text-xs rounded-xl cursor-pointer transition-colors"
												disabled={activeActions[`${entry.id}-arrive`]}
												onclick={() =>
													runDebouncedAction(`${entry.id}-arrive`, () =>
														store
															.confirmArrival(entry.id)
															.catch((err) =>
																alert(err?.data?.message || 'Failed to confirm arrival.')
															)
													)}
											>
												Mark Arrived
											</button>
										{/if}
									{:else if entry.state === 'called'}
										<!-- called -->
										<button
											type="button"
											class="px-4 py-2 bg-emerald-500 hover:bg-emerald-450 active:bg-emerald-600 text-slate-950 font-bold text-xs rounded-xl cursor-pointer transition-colors"
											disabled={activeActions[`${entry.id}-start`]}
											onclick={() =>
												runDebouncedAction(`${entry.id}-start`, () =>
													store
														.startService(entry.id)
														.catch((err) => alert(err?.data?.message || 'Failed to start service.'))
												)}
										>
											Start Service
										</button>
										<button
											type="button"
											class="px-4 py-2 bg-rose-600 hover:bg-rose-500 active:bg-rose-700 text-white font-bold text-xs rounded-xl cursor-pointer transition-colors"
											disabled={activeActions[`${entry.id}-noshow`]}
											onclick={() =>
												runDebouncedAction(`${entry.id}-noshow`, () =>
													store
														.markNoShow(entry.id)
														.catch((err) => alert(err?.data?.message || 'Failed to mark no-show.'))
												)}
										>
											Mark No-Show
										</button>
										<button
											type="button"
											class="px-4 py-2 bg-slate-800 hover:bg-slate-700 active:bg-slate-650 text-slate-300 font-bold text-xs rounded-xl cursor-pointer transition-colors"
											disabled={activeActions[`${entry.id}-skip`]}
											onclick={() =>
												runDebouncedAction(`${entry.id}-skip`, () =>
													store
														.skipEntry(entry.id)
														.catch((err) => alert(err?.data?.message || 'Failed to skip entry.'))
												)}
										>
											Skip Back
										</button>
									{:else if entry.state === 'in_progress'}
										<!-- in_progress -->
										<button
											type="button"
											class="px-5 py-2.5 bg-amber-500 hover:bg-amber-450 active:bg-amber-600 text-slate-950 font-extrabold text-xs rounded-xl cursor-pointer transition-all shadow"
											onclick={() => {
												selectedEntryForCheckout = entry;
											}}
										>
											💳 Complete Service
										</button>
									{:else if entry.state === 'skipped'}
										<!-- skipped -->
										<button
											type="button"
											class="px-4 py-2 bg-amber-500 hover:bg-amber-450 active:bg-amber-600 text-slate-950 font-bold text-xs rounded-xl cursor-pointer transition-colors"
											disabled={activeActions[`${entry.id}-reactivate`]}
											onclick={() =>
												runDebouncedAction(`${entry.id}-reactivate`, () =>
													store
														.reactivateEntry(entry.id)
														.catch((err) => alert(err?.data?.message || 'Failed to reactivate.'))
												)}
										>
											Reactivate
										</button>
									{/if}
								</div>
							</div>
						</div>
					{/each}
				</div>
			{/if}
		</section>
	</main>

	<!-- Checkout Modal Portal -->
	{#if selectedEntryForCheckout}
		<CheckoutModal
			entry={selectedEntryForCheckout}
			{store}
			onClose={() => {
				selectedEntryForCheckout = null;
			}}
		/>
	{/if}
</div>

<style>
	/* Subtle animations for stale delay indicators */
	:global(.animate-pulse-slow) {
		animation: pulse 2.5s cubic-bezier(0.4, 0, 0.6, 1) infinite;
	}

	@keyframes pulse {
		0%,
		100% {
			opacity: 1;
		}
		50% {
			opacity: 0.85;
			border-color: rgba(239, 68, 68, 0.7);
		}
	}
</style>
