<script lang="ts">
	import Icon from '$lib/components/Icon.svelte';
	import { getApiBase } from '$lib/api/client';

	let { data }: { data: any } = $props();

	// Local status so a successful cancel updates the view without a reload.
	let status = $state<string>(data.apt?.status ?? '');
	let confirming = $state(false);
	let cancelling = $state(false);
	let cancelError = $state<string | null>(null);

	const apt = data.apt;

	const dateLabel = apt
		? new Intl.DateTimeFormat('en-IN', {
				weekday: 'long',
				day: 'numeric',
				month: 'long',
				timeZone: apt.timezone || 'Asia/Kolkata'
			}).format(new Date(apt.scheduled_start_at))
		: '';
	const timeLabel = apt
		? new Intl.DateTimeFormat('en-IN', {
				hour: 'numeric',
				minute: '2-digit',
				timeZone: apt.timezone || 'Asia/Kolkata'
			}).format(new Date(apt.scheduled_start_at))
		: '';
	const totalPaise = (apt?.services ?? []).reduce(
		(n: number, s: any) => n + (s.price_paise || 0),
		0
	);

	const statusMeta: Record<string, { label: string; cls: string }> = {
		scheduled: { label: 'Confirmed', cls: 'text-emerald-400 border-emerald-400/25 bg-emerald-400/10' },
		checked_in: { label: 'Checked In', cls: 'text-gold-accent border-gold-accent/25 bg-gold-accent/10' },
		cancelled: { label: 'Cancelled', cls: 'text-red-400 border-red-400/25 bg-red-400/10' },
		no_show: { label: 'Missed', cls: 'text-red-400 border-red-400/25 bg-red-400/10' },
		rescheduled: { label: 'Rescheduled', cls: 'text-muted border-white/10 bg-titanium' }
	};
	const badge = $derived(statusMeta[status] ?? { label: status, cls: 'text-muted border-white/10 bg-titanium' });

	async function cancelAppointment() {
		cancelling = true;
		cancelError = null;
		try {
			const res = await fetch(`${getApiBase()}/v1/appointments/my/cancel`, {
				method: 'POST',
				headers: { 'X-Session-Token': data.token }
			});
			if (res.ok) {
				status = 'cancelled';
				confirming = false;
			} else if (res.status === 409) {
				cancelError = 'This appointment can no longer be cancelled — it may already be checked in.';
			} else {
				cancelError = 'Could not cancel right now. Please try again or contact the shop.';
			}
		} catch {
			cancelError = 'Network error. Please try again.';
		} finally {
			cancelling = false;
		}
	}

	const errorCopy: Record<string, { title: string; body: string }> = {
		invalid_link: {
			title: 'Invalid link',
			body: 'This appointment link is incomplete. Please open it again from your WhatsApp message.'
		},
		expired: {
			title: 'Link expired',
			body: 'This appointment link has expired. If you need help, please contact the shop directly.'
		},
		not_found: {
			title: 'Appointment not found',
			body: 'We could not find this appointment. It may have been removed.'
		},
		unavailable: {
			title: 'Temporarily unavailable',
			body: 'We could not load your appointment right now. Please try again in a moment.'
		}
	};
</script>

<svelte:head>
	<title>{apt ? `Appointment at ${apt.shop_name}` : 'Appointment'} — BarberBase</title>
	<meta name="robots" content="noindex" />
</svelte:head>

<div class="min-h-screen bg-canvas text-primary flex flex-col items-center justify-center p-4 md:p-6">
	<div class="w-full max-w-md bg-matte border border-white/[0.04] rounded-3xl p-6 md:p-8 shadow-2xl space-y-6">
		{#if data.error}
			{@const copy = errorCopy[data.error] ?? errorCopy.unavailable}
			<div class="text-center space-y-4 py-6">
				<div class="mx-auto w-14 h-14 rounded-full bg-canvas/60 border border-white/[0.06] flex items-center justify-center text-muted">
					<Icon name="calendar" size={24} />
				</div>
				<h1 class="text-xl font-extrabold tracking-tight">{copy.title}</h1>
				<p class="text-sm text-muted leading-relaxed">{copy.body}</p>
			</div>
		{:else}
			<header class="text-center space-y-2">
				<p class="font-mono text-[10px] font-medium uppercase tracking-[0.25em] text-dim">
					Your Appointment
				</p>
				<h1 class="text-2xl font-extrabold tracking-tight uppercase">{apt.shop_name}</h1>
				<span class="inline-block text-xs font-bold px-3 py-1 rounded-full border {badge.cls}">
					{badge.label}
				</span>
			</header>

			<div class="bg-canvas/60 border border-white/[0.04] rounded-2xl p-5 space-y-3">
				<div class="flex items-center gap-3">
					<span class="text-gold-accent shrink-0"><Icon name="calendar" size={18} /></span>
					<div>
						<p class="text-sm font-bold">{dateLabel}</p>
						<p class="text-xs text-muted">{timeLabel} · ~{apt.total_duration_minutes} min</p>
					</div>
				</div>
				{#if apt.location_address}
					<div class="flex items-center gap-3 pt-3 border-t border-white/[0.04]">
						<span class="text-muted shrink-0"><Icon name="map-pin" size={18} /></span>
						<p class="text-xs text-muted leading-relaxed">{apt.location_address}</p>
					</div>
				{/if}
			</div>

			<div class="space-y-2">
				<p class="font-mono text-[10px] font-medium uppercase tracking-[0.25em] text-dim">Services</p>
				<ul class="divide-y divide-white/[0.04] border border-white/[0.04] rounded-2xl bg-canvas/40">
					{#each apt.services as svc}
						<li class="flex justify-between items-center px-4 py-3 text-sm">
							<span class="font-semibold">{svc.name}</span>
							<span class="text-muted font-mono text-xs">{svc.duration_minutes} min · ₹{svc.price_paise / 100}</span>
						</li>
					{/each}
					{#if totalPaise > 0}
						<li class="flex justify-between items-center px-4 py-3 text-sm">
							<span class="text-muted">Total</span>
							<span class="font-bold font-mono text-gold-accent">₹{totalPaise / 100}</span>
						</li>
					{/if}
				</ul>
			</div>

			{#if status === 'scheduled'}
				<div class="space-y-3">
					{#if cancelError}
						<p class="text-xs text-red-400 text-center font-semibold">{cancelError}</p>
					{/if}
					{#if confirming}
						<p class="text-xs text-muted text-center">Cancel this appointment? This can't be undone.</p>
						<div class="flex gap-2">
							<button
								class="flex-1 py-3 rounded-full text-sm font-bold border border-white/[0.08] text-primary hover:bg-titanium transition-colors"
								onclick={() => (confirming = false)}
								disabled={cancelling}
							>
								Keep it
							</button>
							<button
								class="flex-1 py-3 rounded-full text-sm font-bold bg-red-400/10 border border-red-400/30 text-red-400 hover:bg-red-400/20 transition-colors"
								onclick={cancelAppointment}
								disabled={cancelling}
							>
								{cancelling ? 'Cancelling…' : 'Yes, cancel'}
							</button>
						</div>
					{:else}
						<button
							class="w-full py-3 rounded-full text-sm font-bold border border-white/[0.08] text-muted hover:text-red-400 hover:border-red-400/30 transition-colors"
							onclick={() => (confirming = true)}
						>
							Cancel Appointment
						</button>
					{/if}
				</div>
			{:else if status === 'cancelled'}
				<p class="text-sm text-muted text-center leading-relaxed">
					This appointment has been cancelled. Want a new slot? Visit
					<a class="text-gold-accent underline" href="/{apt.location_slug}">the shop page</a> to rebook.
				</p>
			{:else if status === 'checked_in'}
				<p class="text-sm text-muted text-center leading-relaxed">
					You're checked in — track your live queue position from the status link in your WhatsApp messages.
				</p>
			{/if}
		{/if}

		<div class="pt-4 border-t border-white/[0.04] text-center">
			<span class="font-mono text-[10px] font-medium text-dim uppercase tracking-[0.25em]">BarberBase</span>
		</div>
	</div>
</div>
