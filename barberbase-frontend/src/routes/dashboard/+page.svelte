<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { QueueStore } from '$lib/stores/queue.svelte';
	import { connectSSE } from '$lib/sse';
	import CheckoutModal from '$lib/components/CheckoutModal.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { formatHHMM } from '$lib';

	// SvelteKit SSR data
	let { data } = $props<{
		data: {
			snapshot: any;
			locationId: string;
			accessToken: string;
			streamToken: string;
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
			initialData.streamToken,
			store,
			initialData.apiBase
		);
	});

	onDestroy(() => {
		if (sseClient) {
			sseClient.close();
		}
	});

	// Today-at-a-glance (from /v1/staff/analytics/daily, loaded server-side)
	const daily = initialData.dailyAnalytics;
	function formatRupees(paise: number | undefined | null): string {
		return `₹${((paise ?? 0) / 100).toLocaleString('en-IN')}`;
	}

	// Shop open/closed + today's hours (public status endpoint, loaded server-side)
	const shopToday = initialData.shopToday;
	const shopHours = shopToday?.business_hours_today;
	const SHOP_STATUS_LABEL: Record<string, string> = {
		open: 'Open',
		closing_soon: 'Closing soon',
		temporarily_closed: 'Temp. closed',
		closed: 'Closed'
	};

	// Who's on shift — staff/members is already fetched for the barber dropdowns;
	// ponytail: status is load-time only (plus local PATCH echo), wire to SSE if live presence matters
	let staffStatuses = $state<Record<string, string>>(
		Object.fromEntries((initialData.staffMembers ?? []).map((m: any) => [m.id, m.status]))
	);
	const onShift = $derived(
		(initialData.staffMembers ?? []).filter(
			(m: any) => staffStatuses[m.id] && staffStatuses[m.id] !== 'offline'
		)
	);
	const PRESENCE_DOT: Record<string, string> = {
		cutting: 'bg-gold-accent',
		idle: 'bg-system-success',
		break: 'bg-system-warning'
	};

	// Barber self-status toggle ('cutting' is system-set at dispatch, never offered here)
	const me = data.staff;
	const isManager = me?.role === 'owner' || me?.role === 'manager';
	// Owner/manager see the full roster incl. offline; barbers see only who's on shift
	const rosterMembers = $derived(isManager ? (initialData.staffMembers ?? []) : onShift);
	const SELF_STATUS_OPTIONS = [
		{ value: 'idle', label: 'Available' },
		{ value: 'break', label: 'Break' },
		{ value: 'offline', label: 'Off' }
	] as const;
	let statusSaving = $state(false);
	async function setMyStatus(status: 'idle' | 'break' | 'offline') {
		if (!me?.staff_member_id || statusSaving || staffStatuses[me.staff_member_id] === status) return;
		const previous = staffStatuses[me.staff_member_id];
		staffStatuses[me.staff_member_id] = status; // optimistic
		statusSaving = true;
		try {
			await store.setBarberStatus(me.staff_member_id, status);
		} catch (err: any) {
			staffStatuses[me.staff_member_id] = previous;
			showToast(err?.data?.message || 'Failed to update your status.');
		} finally {
			statusSaving = false;
		}
	}

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

	// ponytail: two-tap confirm for Call Next — auto-resets after 3s
	let callNextArmed = $state<boolean>(false);
	let callNextTimer: ReturnType<typeof setTimeout> | null = null;

	// Toast for action errors (replaces alert())
	let toastMessage = $state<string>('');
	let toastTimer: ReturnType<typeof setTimeout> | null = null;
	function showToast(msg: string) {
		toastMessage = msg;
		if (toastTimer) clearTimeout(toastTimer);
		toastTimer = setTimeout(() => { toastMessage = ''; }, 4000);
	}

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

	// Flatten catalog variants (contract shape: lowercase groups/variants per openapi.yaml ServiceCatalog)
	const allVariants = $derived(() => {
		if (!data.catalog || !Array.isArray(data.catalog.categories)) return [];
		const list: any[] = [];
		for (const cat of data.catalog.categories) {
			for (const grp of Array.isArray(cat.groups) ? cat.groups : []) {
				for (const vr of Array.isArray(grp.variants) ? grp.variants : []) {
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

	// Next-in-line for Call Next confirmation preview
	const nextInLine = $derived(() => {
		if (!store.snapshot?.entries) return null;
		const waiting = store.snapshot.entries
			.filter((e: any) => e.state === 'waiting')
			.sort((a: any, b: any) => {
				const pgA = typeof a.priority_group === 'number' ? a.priority_group : 100;
				const pgB = typeof b.priority_group === 'number' ? b.priority_group : 100;
				if (pgA !== pgB) return pgA - pgB;
				return new Date(a.joined_at).getTime() - new Date(b.joined_at).getTime();
			});
		return waiting[0] || null;
	});

	function handleCallNext() {
		if (!callNextArmed) {
			callNextArmed = true;
			if (callNextTimer) clearTimeout(callNextTimer);
			callNextTimer = setTimeout(() => { callNextArmed = false; }, 3000);
			return;
		}
		callNextArmed = false;
		if (callNextTimer) clearTimeout(callNextTimer);
		runDebouncedAction('call-next', () =>
			store.callNext().catch((err: any) => {
				if (err?.status === 404) {
					const remote = err?.data?.waiting_remote_count;
					showToast(
						remote > 0
							? `No one has arrived yet — ${remote} still on the way.`
							: 'No customers waiting to call.'
					);
					return;
				}
				showToast(err?.data?.message || 'Failed to call next customer.');
			})
		);
	}

	// Legibility helpers: humanized state labels, presence icons, card tiers.
	const STATE_LABELS: Record<string, string> = {
		in_progress: 'In Chair',
		called: 'Called',
		waiting: 'Waiting',
		skipped: 'Skipped'
	};

	function stateBadgeClass(state: string): string {
		switch (state) {
			case 'in_progress':
				return 'bg-system-success/10 border-system-success/30 text-system-success';
			case 'called':
				return 'bg-gold-accent border-gold-accent text-canvas';
			case 'waiting':
				return 'bg-titanium border-white/[0.08] text-muted';
			default:
				return 'bg-canvas/50 border-white/[0.03] text-dim';
		}
	}

	function presenceMeta(p: string | null | undefined) {
		switch (p) {
			case 'remote':
				return { icon: 'globe' as const, label: 'Remote', cls: 'text-muted' };
			case 'notified':
				return { icon: 'send' as const, label: 'Notified', cls: 'text-muted' };
			case 'on_the_way':
				return { icon: 'arrow-right' as const, label: 'On the Way', cls: 'text-gold-accent' };
			case 'arrived':
				return { icon: 'check' as const, label: 'Arrived', cls: 'text-system-success' };
			case 'snoozed':
				return { icon: 'pause-circle' as const, label: 'Snoozed', cls: 'text-system-warning' };
			default:
				return { icon: 'user' as const, label: 'Walk-in', cls: 'text-dim' };
		}
	}

	// Stale warnings (spec: yellow/red) override the state tier.
	function cardClass(entry: any): string {
		if (entry.stale_warning === 'called_critical' || entry.stale_warning === 'in_progress_critical')
			return 'border-system-error/60 bg-system-error/[0.06] ring-2 ring-system-error/40 animate-pulse-slow';
		if (entry.stale_warning === 'called_warning' || entry.stale_warning === 'in_progress_warning')
			return 'border-system-warning/50 bg-system-warning/[0.05] ring-1 ring-system-warning/25';
		switch (entry.state) {
			case 'in_progress':
				return 'border-system-success/25 bg-surface';
			case 'called':
				return 'border-gold-accent/35 bg-gold-accent/[0.04]';
			case 'skipped':
				return 'border-white/[0.03] bg-matte opacity-60';
			default:
				return 'border-white/[0.03] bg-matte hover:border-white/[0.06]';
		}
	}

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

	// Labeled sections so the queue's priority tiers read at a glance.
	const queueGroups = $derived(() => {
		const buckets: Record<string, any[]> = {
			in_progress: [],
			called: [],
			waiting: [],
			skipped: [],
			other: []
		};
		for (const entry of sortedEntries()) {
			buckets[entry.state in buckets ? entry.state : 'other'].push(entry);
		}
		return [
			{ key: 'in_progress', label: 'Now Serving', cls: 'text-system-success', entries: buckets.in_progress },
			{ key: 'called', label: 'Called', cls: 'text-gold-accent', entries: buckets.called },
			{ key: 'waiting', label: 'Waiting', cls: 'text-muted', entries: buckets.waiting },
			{ key: 'skipped', label: 'Skipped', cls: 'text-dim', entries: buckets.skipped },
			{ key: 'other', label: 'Other', cls: 'text-dim', entries: buckets.other }
		].filter((g) => g.entries.length > 0);
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

<div class="min-h-screen bg-canvas text-primary flex flex-col font-manrope">
	<!-- Error toast -->
	{#if toastMessage}
		<div role="alert" aria-live="assertive" class="fixed top-4 left-1/2 -translate-x-1/2 z-50 bg-matte border border-system-error/40 text-system-error rounded-xl px-5 py-3 text-sm font-medium shadow-lg max-w-md animate-fade-in">
			{toastMessage}
		</div>
	{/if}
	<!-- Top Navigation Header -->
	<header
		class="bg-matte border-b border-white/[0.03] px-6 py-4 flex flex-wrap justify-between items-center gap-4"
	>
		<div class="flex items-center space-x-3">
			<h1 class="text-xl font-extrabold text-gold-accent tracking-wider">BarberBase</h1>
			<span class="text-dim">|</span>
			<span class="text-sm font-semibold text-primary">Staff Dashboard</span>
		</div>

		<!-- Status Indicators -->
		<div class="flex flex-wrap items-center gap-x-4 gap-y-2">
			<!-- SSE Live Sync indicator -->
			<div
				class="flex items-center space-x-1.5 text-xs bg-canvas border border-white/[0.03] rounded-full px-3 py-1 font-semibold"
			>
				<span class="relative flex h-2.5 w-2.5">
					<span
						class="animate-ping absolute inline-flex h-full w-full rounded-full opacity-75 {store.sseConnected
							? 'bg-system-success'
							: 'bg-system-error'}"
					></span>
					<span
						class="relative inline-flex rounded-full h-2.5 w-2.5 {store.sseConnected
							? 'bg-system-success'
							: 'bg-system-error'}"
					></span>
				</span>
				<span class={store.sseConnected ? 'text-system-success/80' : 'text-system-error'}>
					{store.sseConnected ? 'Live' : 'SSE Offline'}
				</span>
			</div>

			<!-- Queue Session Status Badge -->
			{#if store.snapshot}
				<div
					class="text-xs font-bold uppercase tracking-wider px-3 py-1 rounded-full border border-white/[0.05] bg-canvas flex items-center"
				>
					Session:
					<span
						class="ml-1.5 {store.snapshot.session_status === 'active'
							? 'text-system-success/80'
							: 'text-gold-accent'}"
					>
						{#if store.snapshot.session_status === 'closed'}
							No active queue
						{:else}
							{store.snapshot.session_status}
						{/if}
					</span>
				</div>
			{/if}

			<!-- Shop open/closed + today's hours -->
			{#if shopToday?.shop_status}
				<div
					class="text-xs font-bold uppercase tracking-wider px-3 py-1 rounded-full border border-white/[0.05] bg-canvas flex items-center"
				>
					Shop:
					<span
						class="ml-1.5 {shopToday.shop_status === 'open'
							? 'text-system-success/80'
							: shopToday.shop_status === 'closing_soon' || shopToday.shop_status === 'temporarily_closed'
								? 'text-system-warning'
								: 'text-muted'}"
					>
						{SHOP_STATUS_LABEL[shopToday.shop_status] ?? shopToday.shop_status}
					</span>
					{#if shopHours?.opens_at && shopHours?.closes_at}
						<span class="ml-2 text-muted font-medium normal-case tracking-normal">
							{formatHHMM(shopHours.opens_at)} – {formatHHMM(shopHours.closes_at)}
						</span>
					{/if}
				</div>
			{/if}

			<!-- Self status toggle: idle ⇄ break ⇄ offline ('cutting' is system-set, not offered) -->
			{#if me?.staff_member_id && staffStatuses[me.staff_member_id]}
				<div
					class="flex items-center text-xs bg-canvas border border-white/[0.05] rounded-full p-0.5 font-semibold"
					role="group"
					aria-label="My status"
				>
					{#if staffStatuses[me.staff_member_id] === 'cutting'}
						<span class="px-3 py-1 text-gold-accent uppercase tracking-wider text-[10px] font-mono font-bold">
							Cutting
						</span>
					{/if}
					{#each SELF_STATUS_OPTIONS as opt (opt.value)}
						<button
							type="button"
							disabled={statusSaving}
							aria-pressed={staffStatuses[me.staff_member_id] === opt.value}
							class="px-3 py-1 rounded-full cursor-pointer transition-colors {staffStatuses[me.staff_member_id] === opt.value
								? opt.value === 'idle'
									? 'bg-system-success/15 text-system-success'
									: opt.value === 'break'
										? 'bg-system-warning/15 text-system-warning'
										: 'bg-titanium text-primary'
								: 'text-muted hover:text-primary'}"
							onclick={() => setMyStatus(opt.value)}
						>
							{opt.label}
						</button>
					{/each}
				</div>
			{/if}

			<!-- Barber Name -->
			<div class="text-sm text-primary">
				Hello, <span class="font-bold text-primary">{data.snapshot ? 'Barber' : 'Staff'}</span>
			</div>

			<!-- Admin entry point — owner/manager only, absent from DOM for barbers -->
			{#if data.staff?.role === 'owner' || data.staff?.role === 'manager'}
				<a
					href="/admin"
					class="text-xs font-bold uppercase tracking-wider text-gold-accent border border-gold-accent/30 hover:bg-gold-accent/10 rounded-full px-4 min-h-[40px] inline-flex items-center transition-colors"
				>
					Admin
				</a>
			{/if}
		</div>
	</header>

	<!-- Operational Alert Banners -->
	{#if store.snapshot && store.snapshot.session_status === 'ending'}
		<div
			class="bg-system-warning/10 border-b border-system-warning/20 px-6 py-3.5 text-sm text-system-warning flex items-center space-x-3"
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
				status. New online registrations are blocked; please serve the remaining customers.
			</div>
		</div>
	{/if}
	{#if store.snapshot && store.snapshot.session_status === 'closed'}
		<div
			class="bg-matte/40 border-b border-white/[0.01] px-6 py-3.5 text-sm text-muted flex items-center space-x-3"
		>
			<svg
				xmlns="http://www.w3.org/2000/svg"
				class="h-5 w-5 shrink-0 text-dim"
				fill="none"
				viewBox="0 0 24 24"
				stroke="currentColor"
			>
				<path
					stroke-linecap="round"
					stroke-linejoin="round"
					stroke-width="2"
					d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
				/>
			</svg>
			<div>
				No active queue yet. The first check-in will start today's queue.
			</div>
		</div>
	{/if}

	<!-- Today at a glance (from staff/analytics/daily) + who's on shift -->
	{#if daily || rosterMembers.length > 0}
		<section aria-label="Today at a glance" class="max-w-7xl w-full mx-auto px-6 pt-6">
			{#if daily}
			<Card.Root class="rounded-2xl border-white/[0.05] py-0 gap-0">
				<Card.Content
					class="p-0 grid grid-cols-2 sm:grid-cols-4 gap-px bg-white/[0.04] rounded-2xl overflow-hidden [&>div]:bg-matte"
				>
					<div class="px-4 py-3.5" id="glance-revenue">
						<div class="text-xs font-medium text-muted">Revenue Today</div>
						<div class="text-xl font-mono font-bold text-gold-accent mt-0.5">
							{formatRupees(daily.total_revenue_paise)}
						</div>
					</div>
					<div class="px-4 py-3.5" id="glance-served">
						<div class="text-xs font-medium text-muted">Served</div>
						<div class="text-xl font-mono font-bold text-primary mt-0.5">
							{daily.total_visits ?? 0}
						</div>
					</div>
					<div class="px-4 py-3.5" id="glance-avg-wait">
						<div class="text-xs font-medium text-muted">Avg Wait</div>
						<div class="text-xl font-mono font-bold text-primary mt-0.5">
							{daily.average_wait_minutes ?? 0}<span class="text-sm text-muted font-normal ml-1">min</span>
						</div>
					</div>
					<div class="px-4 py-3.5" id="glance-no-shows">
						<div class="text-xs font-medium text-muted">No-shows</div>
						<div class="text-xl font-mono font-bold {(daily.no_show_count ?? 0) > 0 ? 'text-system-warning' : 'text-primary'} mt-0.5">
							{daily.no_show_count ?? 0}
						</div>
					</div>
				</Card.Content>
			</Card.Root>
			{/if}

			{#if rosterMembers.length > 0}
				<!-- Owner/manager see all barbers incl. offline; barbers see on-shift only -->
				<div class="flex flex-wrap items-center gap-2 {daily ? 'mt-3' : ''} px-1">
					<span class="text-xs font-medium text-muted">{isManager ? 'Staff:' : 'On shift:'}</span>
					{#each rosterMembers as member (member.id)}
						{@const status = staffStatuses[member.id]}
						<Badge
							variant="secondary"
							class="gap-1.5 border border-white/[0.05] {status === 'offline' ? 'opacity-50' : ''}"
						>
							<span class="size-1.5 rounded-full {PRESENCE_DOT[status] ?? 'bg-dim'}"></span>
							{member.name}
							<span class="text-muted">{status === 'break' ? 'on break' : status}</span>
						</Badge>
					{/each}
				</div>
			{/if}
		</section>
	{/if}

	<main id="main-content" class="flex-1 max-w-7xl w-full mx-auto p-6 flex flex-col lg:flex-row gap-6">
		<!-- Left Side: Queue Controls and Add Walk-in -->
		<section class="w-full lg:w-1/3 flex flex-col space-y-6">
			<!-- Primary Dispatch Console -->
			<div class="bg-matte border border-white/[0.03] rounded-2xl p-6 shadow-lg">
				<h2 class="text-lg font-bold text-primary mb-4 tracking-wide">Queue Controller</h2>

				<div class="space-y-4">
					<!-- BIG Call Next Button (two-tap confirm) -->
					<button
						type="button"
						class="w-full py-5 bg-gold-accent {callNextArmed ? 'ring-2 ring-gold-accent/50 brightness-110' : ''} hover:brightness-110 active:brightness-90 active:scale-[0.99] disabled:opacity-40 disabled:hover:brightness-100 text-canvas font-black text-xl rounded-2xl transition-all duration-150 shadow-[0_0_12px_rgba(200,169,107,0.15)] cursor-pointer flex flex-col items-center justify-center space-y-1"
						disabled={activeActions['call-next'] || store.snapshot?.session_status === 'closed'}
						onclick={handleCallNext}
					>
						{#if callNextArmed}
							<span>TAP AGAIN TO CONFIRM</span>
							{#if nextInLine()}
								<span class="text-xs font-semibold opacity-85">
									Next: #{nextInLine()?.token_number} — {nextInLine()?.customer?.name || 'Walk-in'}
								</span>
							{:else}
								<span class="text-xs font-semibold opacity-85">No one waiting</span>
							{/if}
						{:else}
							<span>CALL NEXT CLIENT</span>
							<span class="text-xs font-semibold opacity-85">{activeCount} in queue</span>
						{/if}
					</button>

					<!-- Total wait & count summary stats -->
					<div class="grid grid-cols-2 gap-3 pt-2">
						<div class="bg-canvas border border-white/[0.03] rounded-xl p-3.5 text-center">
							<div class="text-xs font-medium text-muted">Total Active</div>
							<div class="text-2xl font-mono font-bold text-primary mt-1">{activeCount}</div>
						</div>
						<div class="bg-canvas border border-white/[0.03] rounded-xl p-3.5 text-center">
							<div class="text-xs font-medium text-muted">Waiting</div>
							<div class="text-2xl font-mono font-bold text-primary mt-1">{store.snapshot?.entries?.filter((e: any) => e.state === 'waiting').length || 0}</div>
						</div>
					</div>
				</div>
			</div>

			<!-- Add Walk-in Console Panel -->
			<div class="bg-matte border border-white/[0.03] rounded-2xl p-6 shadow-lg">
				<div class="flex justify-between items-center">
					<h2 class="text-lg font-bold text-primary tracking-wide">Add Walk-in Client</h2>
					<button
						type="button"
						class="px-3 py-1.5 text-xs font-bold rounded-xl border border-white/[0.05] hover:bg-titanium transition-colors"
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
						class="space-y-4 pt-4 border-t border-white/[0.03] mt-4 transition-all duration-200"
					>
						<!-- Name -->
						<div>
							<label for="walk-in-name" class="block text-xs font-medium text-muted mb-1"
								>Customer Name (Optional)</label
							>
							<input
								type="text"
								id="walk-in-name"
								placeholder="e.g. Rahul, Guest, Uncle"
								maxlength="80"
								class="w-full bg-canvas border border-white/[0.03] rounded-xl px-3 py-2 text-sm text-primary focus:outline-none focus:border-gold-accent placeholder:text-dim"
								bind:value={walkInName}
							/>
						</div>

						<!-- Phone -->
						<div>
							<label for="walk-in-phone" class="block text-xs font-medium text-muted mb-1"
								>Phone Number (Optional)</label
							>
							<input
								type="tel"
								id="walk-in-phone"
								placeholder="e.g. 9876543210"
								class="w-full bg-canvas border border-white/[0.03] rounded-xl px-3 py-2 text-sm text-primary focus:outline-none focus:border-gold-accent placeholder:text-dim"
								bind:value={walkInPhone}
							/>
							<span class="text-[10px] text-dim mt-0.5 block"
								>10-digit number will automatically prefix with +91.</span
							>
						</div>

						<div class="grid grid-cols-2 gap-3">
							<!-- Party Size -->
							<div>
								<label for="party-size" class="block text-xs font-medium text-muted mb-1"
									>Party Size</label
								>
								<input
									type="number"
									id="party-size"
									min="1"
									max="10"
									class="w-full bg-canvas border border-white/[0.03] rounded-xl px-3 py-2 text-sm text-primary focus:outline-none focus:border-gold-accent"
									bind:value={walkInPartySize}
								/>
							</div>

							<!-- Barber -->
							<div>
								<label for="walk-in-barber" class="block text-xs font-medium text-muted mb-1"
									>Preferred Barber</label
								>
								<select
									id="walk-in-barber"
									class="w-full bg-canvas border border-white/[0.03] rounded-xl px-3 py-2 text-sm text-primary focus:outline-none focus:border-gold-accent"
									bind:value={walkInBarberId}
								>
									<option value="">-- Auto Route --</option>
									<!-- ponytail: on-shift only — requesting an offline barber is a dead end -->
									{#each onShift as member}
										<option value={member.id}>{member.name}</option>
									{/each}
								</select>
							</div>
						</div>

						<!-- Services (Variant Checklist) -->
						<div class="space-y-1">
							<span class="block text-xs font-medium text-muted mb-1.5"
								>Select Service Variants (Required)</span
							>
							<div
								class="max-h-48 overflow-y-auto bg-canvas border border-white/[0.03] rounded-xl p-3 space-y-2 divide-y divide-white/[0.04]"
							>
								{#if allVariants().length === 0}
								<p class="text-xs text-dim py-2 text-center">No services configured. Add services in <a href="/admin/services" class="text-gold-accent hover:underline">Admin → Services</a>.</p>
							{:else}
								{#each allVariants() as v}
									<label class="flex items-start space-x-3 pt-2 first:pt-0 cursor-pointer">
										<input
											type="checkbox"
											value={v.id}
											class="mt-1 rounded text-gold-accent bg-matte border-white/[0.03] focus:ring-offset-canvas"
											bind:group={walkInSelectedVariants}
										/>
										<div class="text-xs">
											<div class="font-bold text-primary">{v.name}</div>
											<div class="text-[10px] text-dim">
												{v.categoryName} • {v.duration} min • {formatCurrency(v.price)}
											</div>
										</div>
									</label>
								{/each}
							{/if}
							</div>
						</div>

						<!-- Error Display -->
						{#if walkInError}
							<div
								class="bg-system-error/10 border border-system-error/30 rounded-xl p-3 text-xs text-system-error flex items-start space-x-2"
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
							class="w-full bg-gold-accent hover:brightness-110 active:brightness-90 text-canvas font-bold py-2.5 rounded-xl transition-all duration-150 text-sm cursor-pointer"
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
				<h2 class="text-lg font-bold text-primary tracking-wide">Live Queue</h2>
				<span class="text-xs text-muted font-semibold"
					>{sortedEntries().length} active items</span
				>
			</div>

			{#if sortedEntries().length === 0}
				<div
					class="bg-matte border border-white/[0.03] rounded-2xl p-12 text-center text-muted flex flex-col items-center justify-center space-y-3"
				>
					<svg
						xmlns="http://www.w3.org/2000/svg"
						class="h-12 w-12 text-dim"
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
					<span class="text-sm font-semibold text-muted">The queue is currently empty.</span>
					<span class="text-xs text-dim"
						>Tap "Call Next" or add a Walk-in to get started.</span
					>
				</div>
			{:else}
				<div class="space-y-6" aria-live="polite" aria-relevant="additions removals">
					{#each queueGroups() as group (group.key)}
						<div class="space-y-3">
							<!-- Section header: the state tier reads before any card detail -->
							<div class="flex items-center gap-3">
								<span class="font-mono text-[10px] font-medium uppercase tracking-[0.25em] {group.cls}">
									{group.label}
								</span>
								<span class="font-mono text-[10px] text-dim">{group.entries.length}</span>
								<div class="flex-1 border-t border-white/[0.04]"></div>
							</div>

							{#each group.entries as entry (entry.id)}
						<!-- Queue Entry Card: tier tint by state, stale warnings override -->
						<div
							class="border rounded-2xl p-4 shadow-md transition-all duration-300 flex flex-col md:flex-row justify-between gap-4 {cardClass(entry)}"
						>
							<!-- Left Column: Token + Customer Details -->
							<div class="flex-1 space-y-3">
								<div class="flex items-center space-x-2 flex-wrap gap-y-1">
									<!-- Token Badge -->
									<span
										class="bg-canvas border border-white/[0.05] text-gold-accent text-base font-mono font-bold px-3 py-1 rounded-xl"
									>
										#{entry.token_number}
									</span>

									<!-- State Status Badge -->
									<span
										class="text-[10px] font-mono font-bold uppercase tracking-wider px-2 py-0.5 rounded-md border {stateBadgeClass(entry.state)}"
									>
										{STATE_LABELS[entry.state] || entry.state}
									</span>

									<!-- Presence Badge -->
									{#if true}
										{@const presence = presenceMeta(entry.presence_state)}
										<span
											class="text-xs font-medium bg-canvas border border-white/[0.03] rounded-lg px-2 py-0.5 flex items-center gap-1.5 {presence.cls}"
										>
											<Icon name={presence.icon} size={13} />
											{presence.label}
										</span>
									{/if}

									<!-- Stale Warning urgency banner -->
									{#if entry.stale_warning === 'called_critical' || entry.stale_warning === 'in_progress_critical'}
										<span
											class="text-[10px] font-mono font-bold tracking-wider px-2 py-0.5 rounded bg-system-error text-canvas animate-pulse"
										>
											DELAYED
										</span>
									{/if}
								</div>

								<!-- Customer profile details -->
								<div>
									<div class="font-extrabold text-base text-primary">
										{entry.customer?.name || 'Walk-in Customer'}
									</div>
									<div class="flex items-center space-x-2 text-xs text-muted mt-1">
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
										class="bg-canvas/40 border border-white/[0.01] rounded-xl p-2.5 text-xs text-primary space-y-1"
									>
										<span
											class="font-bold text-gold-accent text-[10px] uppercase tracking-wider block"
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
											class="text-[10px] bg-canvas border border-white/[0.05] rounded-lg px-2.5 py-1 text-primary"
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
										class="block text-[10px] font-medium text-dim mb-1">Assigned Barber</label
									>
									<select
										id="barber-select-{entry.id}"
										class="bg-canvas border border-white/[0.05] rounded-lg px-2 py-1.5 text-xs text-primary focus:outline-none focus:border-gold-accent w-full"
										value={entry.assigned_barber_id || ''}
										onchange={(e) => {
											const barberId = e.currentTarget.value;
											if (barberId) {
												runDebouncedAction(`${entry.id}-reassign`, () =>
													store
														.reassignBarber(entry.id, barberId)
														.catch((err) => showToast(err?.data?.message || 'Failed to reassign.'))
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

								<!-- Action Buttons: one dominant primary per state, secondaries ghost -->
								<div class="flex flex-wrap gap-2 justify-end w-full">
									{#if entry.state === 'waiting'}
										{#if entry.presence_state === 'arrived'}
											<!-- waiting + presence=arrived -->
											<button
												type="button"
												class="flex-1 md:flex-none min-h-[48px] px-5 bg-system-success hover:brightness-110 active:brightness-90 active:scale-[0.98] text-canvas font-extrabold text-sm rounded-xl cursor-pointer transition-all"
												disabled={activeActions[`${entry.id}-start`]}
												onclick={() =>
													runDebouncedAction(`${entry.id}-start`, () =>
														store
															.startService(entry.id)
															.catch((err) =>
																showToast(err?.data?.message || 'Failed to start service.')
															)
													)}
											>
												Direct Start
											</button>
											<button
												type="button"
												class="min-h-[48px] px-4 bg-transparent border border-white/[0.06] hover:bg-titanium text-muted hover:text-primary font-bold text-xs rounded-xl cursor-pointer transition-colors"
												disabled={activeActions[`${entry.id}-skip`]}
												onclick={() =>
													runDebouncedAction(`${entry.id}-skip`, () =>
														store
															.skipEntry(entry.id)
															.catch((err) => showToast(err?.data?.message || 'Failed to skip entry.'))
													)}
											>
												Skip
											</button>
										{:else}
											<!-- waiting + presence≠arrived -->
											<button
												type="button"
												class="min-h-[48px] px-4 bg-transparent border border-white/[0.06] hover:bg-titanium text-muted hover:text-primary font-bold text-xs rounded-xl cursor-pointer transition-colors"
												disabled={activeActions[`${entry.id}-skip`]}
												onclick={() =>
													runDebouncedAction(`${entry.id}-skip`, () =>
														store
															.skipEntry(entry.id)
															.catch((err) => showToast(err?.data?.message || 'Failed to skip entry.'))
													)}
											>
												Skip
											</button>
											<button
												type="button"
												class="flex-1 md:flex-none min-h-[48px] px-5 bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] text-canvas font-extrabold text-sm rounded-xl cursor-pointer transition-all"
												disabled={activeActions[`${entry.id}-arrive`]}
												onclick={() =>
													runDebouncedAction(`${entry.id}-arrive`, () =>
														store
															.confirmArrival(entry.id)
															.catch((err) =>
																showToast(err?.data?.message || 'Failed to confirm arrival.')
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
											class="flex-1 md:flex-none min-h-[48px] px-5 bg-system-success hover:brightness-110 active:brightness-90 active:scale-[0.98] text-canvas font-extrabold text-sm rounded-xl cursor-pointer transition-all"
											disabled={activeActions[`${entry.id}-start`]}
											onclick={() =>
												runDebouncedAction(`${entry.id}-start`, () =>
													store
														.startService(entry.id)
														.catch((err) => showToast(err?.data?.message || 'Failed to start service.'))
												)}
										>
											Start Service
										</button>
										<button
											type="button"
											class="min-h-[48px] px-4 bg-system-error/10 border border-system-error/30 hover:bg-system-error hover:text-canvas text-system-error font-bold text-xs rounded-xl cursor-pointer transition-colors"
											disabled={activeActions[`${entry.id}-noshow`]}
											onclick={() =>
												runDebouncedAction(`${entry.id}-noshow`, () =>
													store
														.markNoShow(entry.id)
														.catch((err) => showToast(err?.data?.message || 'Failed to mark no-show.'))
												)}
										>
											Mark No-Show
										</button>
										<button
											type="button"
											class="min-h-[48px] px-4 bg-transparent border border-white/[0.06] hover:bg-titanium text-muted hover:text-primary font-bold text-xs rounded-xl cursor-pointer transition-colors"
											disabled={activeActions[`${entry.id}-skip`]}
											onclick={() =>
												runDebouncedAction(`${entry.id}-skip`, () =>
													store
														.skipEntry(entry.id)
														.catch((err) => showToast(err?.data?.message || 'Failed to skip entry.'))
												)}
										>
											Skip Back
										</button>
									{:else if entry.state === 'in_progress'}
										<!-- in_progress -->
										<button
											type="button"
											class="flex-1 md:flex-none min-h-[48px] px-6 bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] text-canvas font-extrabold text-sm rounded-xl cursor-pointer transition-all shadow-[0_0_12px_rgba(200,169,107,0.15)]"
											onclick={() => {
												selectedEntryForCheckout = entry;
											}}
										>
											Complete Service
										</button>
									{:else if entry.state === 'skipped'}
										<!-- skipped -->
										<button
											type="button"
											class="min-h-[48px] px-5 bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] text-canvas font-bold text-sm rounded-xl cursor-pointer transition-all"
											disabled={activeActions[`${entry.id}-reactivate`]}
											onclick={() =>
												runDebouncedAction(`${entry.id}-reactivate`, () =>
													store
														.reactivateEntry(entry.id)
														.catch((err) => showToast(err?.data?.message || 'Failed to reactivate.'))
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
