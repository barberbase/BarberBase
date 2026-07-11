<script lang="ts">
	import { onMount } from 'svelte';
	import { getApiBase } from '$lib/api/client';
	import Icon from '$lib/components/Icon.svelte';
	import QueueTimeline from '$lib/components/QueueTimeline.svelte';

	// SvelteKit SSR data
	let { data } = $props<{
		data: {
			entry: any | null;
			token: string | null;
			locationId: string | null;
			error: 'invalid_link' | 'expired' | 'not_found' | null;
		};
	}>();

	// Helper to resolve API base URL dynamically
	const getClientApiBase = () => {
		if (typeof window !== 'undefined') {
			const hostname = window.location.hostname;
			if (hostname === 'localhost' || hostname === '127.0.0.1') {
				return 'http://127.0.0.1:9090';
			}
		}
		return getApiBase();
	};

	// Local state management
	const initialData = $state.snapshot(data);
	const token = initialData.token;
	const locationId = initialData.locationId;
	const initialError = initialData.error;

	let currentEntry = $state<any>(initialData.entry);
	let localQueueVersion = $state<number>(initialData.entry?.queue_version ?? 0);

	let pinInput = $state<string>('');
	let pinError = $state<string | null>(null);
	let pinAttemptsRemaining = $state<number | null>(null);

	let gpsLoading = $state<boolean>(false);
	let gpsError = $state<string | null>(null);

	let actionError = $state<string | null>(null);
	let feedbackRating = $state<number>(0);
	let feedbackSubmitted = $state<boolean>(false);
	let feedbackMessage = $state<string | null>(null);
	let isSubmitting = $state<boolean>(false);

	let scheduleRefetch: (() => void) | null = null;

	onMount(() => {
		if (!token || !locationId) return;

		const apiBase = getClientApiBase();
		const url = `${apiBase}/v1/stream/${locationId}?token=${encodeURIComponent(token)}`;
		let es: EventSource | null = null;
		let reconnectDelay = 1000;
		let idleTimer: ReturnType<typeof setTimeout>;
		let stopped = false;

		const resetIdle = () => {
			clearTimeout(idleTimer);
			idleTimer = setTimeout(
				() => {
					es?.close();
					stopped = true;
				},
				4 * 60 * 60 * 1000
			); // 4 hours idle timeout
		};

		// pendingVersion = highest queue_version seen on the SSE stream. The gate
		// (localQueueVersion) only advances to it after a successful canonical refetch,
		// so a fetch that drops on spotty 4G leaves the gate open and the next
		// heartbeat (which also carries queue_version) retries within 30s. my-status's
		// body has no queue_version, so the version must come from the event payload.
		let pendingVersion = localQueueVersion;
		const refetch = async () => {
			const versionAtFetch = pendingVersion;
			try {
				const apiBaseUrl = getClientApiBase();
				const res = await fetch(`${apiBaseUrl}/v1/queue/my-status`, {
					headers: { 'X-Session-Token': token }
				});
				if (res.ok) {
					const freshData = await res.json();
					currentEntry = freshData;
					localQueueVersion = Math.max(localQueueVersion, versionAtFetch);
					resetIdle();
					// A burst of events during this in-flight fetch may have advanced
					// pendingVersion past what we just confirmed — chase it now instead of
					// waiting for the next event/heartbeat.
					if (pendingVersion > localQueueVersion) {
						scheduleRefetch?.();
					}
				}
			} catch (err) {
				console.error('[SSE Client] Refetch failed:', err);
			}
		};

		// The 500ms debounce is load-bearing: refetch's in-flight chase reschedules through
		// here, so on a sustained burst the chase coalesces instead of firing per event. If this
		// is ever changed to fire immediately, the chase becomes a tight refetch loop on 4G.
		let debounceTimer: ReturnType<typeof setTimeout>;
		scheduleRefetch = () => {
			clearTimeout(debounceTimer);
			debounceTimer = setTimeout(refetch, 500);
		};

		const connect = () => {
			if (stopped) return;
			if (es) {
				es.close();
			}

			es = new EventSource(url);

			// Resync canonical state on every (re)connect — recovers any mutation missed
			// while the stream was down, independent of the version gate.
			es.onopen = () => {
				scheduleRefetch?.();
			};

			es.addEventListener('queue_changed', (e) => {
				try {
					const eventData = JSON.parse(e.data);
					if (eventData.queue_version > localQueueVersion) {
						pendingVersion = Math.max(pendingVersion, eventData.queue_version);
						scheduleRefetch?.();
					}
				} catch (err) {
					console.error('[SSE] Failed to parse queue_changed event data:', err);
				}
			});

			es.addEventListener('heartbeat', (e) => {
				try {
					const eventData = JSON.parse(e.data);
					if (eventData.queue_version && eventData.queue_version > localQueueVersion) {
						pendingVersion = Math.max(pendingVersion, eventData.queue_version);
						scheduleRefetch?.();
					}
				} catch (err) {
					console.error('[SSE] Failed to parse heartbeat event data:', err);
				}
			});

			es.onerror = () => {
				es?.close();
				if (!stopped) {
					setTimeout(() => {
						reconnectDelay = Math.min(reconnectDelay * 2, 30000);
						connect();
					}, reconnectDelay);
				}
			};

			es.onopen = () => {
				reconnectDelay = 1000;
				refetch(); // fetch immediately on connect/reconnect
			};
		};

		const handleVisibility = () => {
			if (document.hidden) {
				es?.close();
				es = null;
			} else {
				connect();
			}
		};

		document.addEventListener('visibilitychange', handleVisibility);

		resetIdle();
		connect();

		return () => {
			stopped = true;
			es?.close();
			clearTimeout(debounceTimer);
			clearTimeout(idleTimer);
			document.removeEventListener('visibilitychange', handleVisibility);
		};
	});

	// Rest REST API Call functions
	const handleOnTheWay = async () => {
		if (!token || isSubmitting) return;
		isSubmitting = true;
		actionError = null;

		try {
			const apiBase = getClientApiBase();
			const res = await fetch(`${apiBase}/v1/queue/on-the-way`, {
				method: 'POST',
				headers: { 'X-Session-Token': token }
			});
			if (res.ok) {
				// Optimistically set presence_state then refetch
				if (currentEntry) {
					currentEntry.presence_state = 'on_the_way';
				}
				scheduleRefetch?.();
			} else {
				const errData = await res.json().catch(() => ({ message: 'Action failed' }));
				actionError = errData.message || 'Failed to update status to On The Way.';
			}
		} catch (err) {
			actionError = 'Network error. Please try again.';
		} finally {
			isSubmitting = false;
		}
	};

	const handleConfirmArrivalPin = async (e: Event) => {
		e.preventDefault();
		if (!token || isSubmitting) return;
		isSubmitting = true;
		pinError = null;

		try {
			const apiBase = getClientApiBase();
			const res = await fetch(`${apiBase}/v1/queue/confirm-arrival`, {
				method: 'POST',
				headers: {
					'X-Session-Token': token,
					'Content-Type': 'application/json'
				},
				body: JSON.stringify({ method: 'pin', pin: pinInput })
			});

			if (res.ok) {
				pinInput = '';
				scheduleRefetch?.();
			} else {
				const errData = await res.json();
				if (res.status === 400) {
					pinAttemptsRemaining = errData.attempts_remaining ?? null;
					pinError = `Incorrect PIN. ${pinAttemptsRemaining !== null ? pinAttemptsRemaining + ' attempts remaining.' : ''}`;
				} else {
					pinError = errData.message || 'Failed to verify PIN.';
				}
			}
		} catch (err) {
			pinError = 'Network error. Please try again.';
		} finally {
			isSubmitting = false;
		}
	};

	const handleConfirmArrivalGps = async () => {
		if (!token || isSubmitting) return;
		gpsLoading = true;
		gpsError = null;

		if (!navigator.geolocation) {
			gpsError = 'Geolocation is not supported by this browser.';
			gpsLoading = false;
			return;
		}

		navigator.geolocation.getCurrentPosition(
			async (position) => {
				try {
					const apiBase = getClientApiBase();
					const res = await fetch(`${apiBase}/v1/queue/confirm-arrival`, {
						method: 'POST',
						headers: {
							'X-Session-Token': token,
							'Content-Type': 'application/json'
						},
						body: JSON.stringify({
							method: 'geolocation',
							latitude: position.coords.latitude,
							longitude: position.coords.longitude,
							accuracy_metres: position.coords.accuracy
						})
					});

					if (res.ok) {
						scheduleRefetch?.();
					} else {
						const errData = await res.json();
						if (res.status === 422) {
							gpsError = 'GPS accuracy too low. Please enter the PIN instead.';
						} else {
							gpsError = errData.message || 'Failed to verify location.';
						}
					}
				} catch (err) {
					gpsError = 'Network error verifying location. Use PIN instead.';
				} finally {
					gpsLoading = false;
				}
			},
			(error) => {
				gpsLoading = false;
				if (error.code === error.PERMISSION_DENIED) {
					gpsError = 'Location permission denied. Use the PIN.';
				} else {
					gpsError = 'Could not retrieve location. Use the PIN.';
				}
			},
			{ enableHighAccuracy: true, timeout: 10000, maximumAge: 0 }
		);
	};

	const handleCancel = async () => {
		if (!token || isSubmitting) return;
		isSubmitting = true;
		actionError = null;

		try {
			const apiBase = getClientApiBase();
			const res = await fetch(`${apiBase}/v1/queue/cancel`, {
				method: 'POST',
				headers: { 'X-Session-Token': token }
			});
			if (res.ok) {
				scheduleRefetch?.();
			} else {
				const errData = await res.json().catch(() => ({ message: 'Cannot cancel' }));
				actionError = errData.message || 'Failed to cancel your spot.';
			}
		} catch (err) {
			actionError = 'Network error. Please try again.';
		} finally {
			isSubmitting = false;
		}
	};

	const handleFeedback = async (rating: number) => {
		if (!token || feedbackSubmitted) return;
		feedbackRating = rating;
		feedbackMessage = null;

		try {
			const apiBase = getClientApiBase();
			const res = await fetch(`${apiBase}/v1/queue/feedback`, {
				method: 'POST',
				headers: {
					'X-Session-Token': token,
					'Content-Type': 'application/json'
				},
				body: JSON.stringify({ rating })
			});

			if (res.ok || res.status === 201) {
				feedbackSubmitted = true;
				feedbackMessage = 'Thank you for your feedback!';
			} else if (res.status === 409) {
				feedbackSubmitted = true;
				feedbackMessage = 'Feedback already submitted.';
			} else {
				const errData = await res.json().catch(() => ({}));
				feedbackMessage = errData.message || 'Failed to submit feedback.';
			}
		} catch (err) {
			feedbackMessage = 'Network error. Could not save feedback.';
		}
	};
</script>

<svelte:head>
	<title>Live Queue Status — BarberBase</title>
</svelte:head>

<div
	class="min-h-dvh bg-canvas text-primary flex flex-col items-center justify-center p-4 md:p-6 font-manrope"
>
	<!-- solid matte instead of backdrop-blur: blur-xl janks on low-end Android -->
	<div
		class="w-full max-w-md bg-matte border border-white/[0.03] rounded-3xl p-6 shadow-2xl space-y-6 relative overflow-hidden"
		aria-live="polite" aria-atomic="true"
	>
		<!-- Subtle ambient backdrop light glow -->
		<div
			class="absolute -top-24 -left-24 w-48 h-48 bg-gold-accent/10 rounded-full blur-3xl pointer-events-none"
		></div>
		<div
			class="absolute -bottom-24 -right-24 w-48 h-48 bg-gold-accent/5 rounded-full blur-3xl pointer-events-none"
		></div>

		<!-- ERROR PAGES -->
		{#if initialError === 'invalid_link'}
			<div class="text-center py-10 space-y-4">
				<div class="mx-auto w-16 h-16 rounded-full bg-canvas/60 border border-white/[0.06] flex items-center justify-center text-system-error">
					<Icon name="alert" size={28} />
				</div>
				<h1 class="text-xl font-extrabold text-primary">Invalid Link</h1>
				<p class="text-sm text-muted">
					This link is not valid. Please request a new one via WhatsApp.
				</p>
			</div>
		{:else if initialError === 'expired'}
			<div class="text-center py-10 space-y-4">
				<div class="mx-auto w-16 h-16 rounded-full bg-canvas/60 border border-white/[0.06] flex items-center justify-center text-system-warning">
					<Icon name="clock" size={28} />
				</div>
				<h1 class="text-xl font-extrabold text-primary">Link Expired</h1>
				<p class="text-sm text-muted">
					Your session has expired (links are valid for 23 hours).
				</p>
			</div>
		{:else if initialError === 'not_found'}
			<div class="text-center py-10 space-y-4">
				<div class="mx-auto w-16 h-16 rounded-full bg-canvas/60 border border-white/[0.06] flex items-center justify-center text-muted">
					<Icon name="inbox" size={28} />
				</div>
				<h1 class="text-xl font-extrabold text-primary">Inactive Entry</h1>
				<p class="text-sm text-muted">This queue entry is no longer active.</p>
			</div>
		{:else if currentEntry}
			<!-- HEADER SECTION -->
			<div class="flex justify-between items-center border-b border-white/[0.03] pb-4">
				<div>
					<h2 class="text-xs font-bold text-gold-accent uppercase tracking-widest">
						{currentEntry.shop_name || 'BarberBase'}
					</h2>
					<p class="text-[10px] text-muted mt-0.5">
						{currentEntry.location_name || 'Salon Location'}
					</p>
				</div>
				<span
					class="bg-gold-accent/10 border border-gold-accent/20 text-gold-accent text-xs font-black px-3.5 py-1.5 rounded-full"
				>
					Token #{currentEntry.token_number}
				</span>
			</div>

			<!-- JOURNEY TIMELINE -->
			<QueueTimeline state={currentEntry.state} presenceState={currentEntry.presence_state} />

			<!-- NORMAL STATES CONTROLLER -->
			<!-- {#key} remounts the block on any state/presence change so the CSS
			     entrance runs — a soft rise instead of a hard DOM swap. -->
			{#key `${currentEntry.state}:${currentEntry.presence_state}`}
			<div class="state-enter">
			{#if currentEntry.state === 'completed'}
				<!-- STATE 6 — Completed -->
				<div class="text-center py-6 space-y-4">
					<!-- Peak-end moment: one expanding gold halo on arrival, then the slow
					     float. CSS-only; both disabled under prefers-reduced-motion. -->
					<div class="mx-auto w-16 h-16 rounded-full bg-gold-accent/10 border border-gold-accent/25 flex items-center justify-center text-gold-accent animate-float-slow halo-once relative">
						<Icon name="sparkles" size={28} />
					</div>
					<h1 class="text-2xl font-black text-primary">All done — looking sharp!</h1>
					<p class="text-sm text-muted">
						Thanks for visiting {currentEntry.shop_name || 'us'}. See you next time.
					</p>

					<!-- Feedback Star Widget -->
					<div class="bg-canvas/40 border border-white/[0.02] rounded-2xl p-5 space-y-3.5 mt-2">
						<span class="text-xs font-bold text-muted uppercase tracking-wider block"
							>Rate your experience</span
						>
						<div class="flex justify-center space-x-2.5">
							{#each [1, 2, 3, 4, 5] as star}
								<button
									type="button"
									class="text-3xl focus:outline-none transition-transform active:scale-125 duration-100 min-h-[48px] min-w-[48px] flex items-center justify-center cursor-pointer"
									onclick={() => handleFeedback(star)}
									disabled={feedbackSubmitted}
								>
									<span class={star <= (feedbackRating || 0) ? 'text-gold-accent' : 'text-dim'}
										>★</span
									>
								</button>
							{/each}
						</div>
						{#if feedbackMessage}
							<p class="text-xs font-semibold text-gold-accent mt-2">{feedbackMessage}</p>
						{/if}
					</div>
				</div>
			{:else if currentEntry.state === 'called'}
				<!-- STATE 4 — Called -->
				<div
					class="text-center py-8 space-y-4 bg-gold-accent/10 border border-gold-accent/30 rounded-3xl p-6 ring-2 ring-gold-accent/20"
				>
					<div class="mx-auto w-16 h-16 rounded-full bg-gold-accent text-canvas flex items-center justify-center shadow-[0_0_12px_rgba(200,169,107,0.15)] motion-safe:animate-pulse">
						<Icon name="bell" size={28} />
					</div>
					<h1 class="text-2xl font-black text-gold-accent">It's your turn!</h1>
					<!-- Staff call out the token number — make it readable from arm's length. -->
					<div class="font-mono font-bold text-5xl text-primary tracking-wider" aria-label="Your token number">
						#{currentEntry.token_number}
					</div>
					<p class="text-sm text-gold-accent">
						Go to the counter now and show this screen. Your spot is held for a few minutes.
					</p>
				</div>
			{:else if currentEntry.state === 'in_progress'}
				<!-- STATE 5 — In Progress -->
				<div
					class="text-center py-8 space-y-4 bg-system-success/10 border border-system-success/30 rounded-3xl p-6"
				>
					<div class="mx-auto w-16 h-16 rounded-full bg-system-success/10 border border-system-success/30 flex items-center justify-center text-system-success">
						<Icon name="scissors" size={28} />
					</div>
					<h1 class="text-2xl font-black text-system-success">In the chair</h1>
					<p class="text-sm text-system-success">You're being served now — enjoy!</p>
				</div>
			{:else if currentEntry.state === 'cancelled' || currentEntry.state === 'expired'}
				<!-- STATE — Terminal / Cancelled -->
				<div class="text-center py-6 space-y-4">
					<div class="mx-auto w-16 h-16 rounded-full bg-canvas/60 border border-white/[0.06] flex items-center justify-center text-system-error">
						<Icon name="x-circle" size={28} />
					</div>
					<h1 class="text-2xl font-black text-primary">Queue Entry Ended</h1>
					<p class="text-sm text-muted">
						{currentEntry.state === 'cancelled'
							? 'Your spot was cancelled. Visit the shop to rejoin.'
							: 'This session has ended. Please ask staff for assistance.'}
					</p>
				</div>
			{:else if currentEntry.presence_state === 'snoozed' || currentEntry.state === 'skipped' || currentEntry.state === 'no_show'}
				<!-- STATE 7 — Spot Paused -->
				<div class="text-center py-6 space-y-4">
					<div class="mx-auto w-16 h-16 rounded-full bg-canvas/60 border border-white/[0.06] flex items-center justify-center text-system-warning">
						<Icon name="pause-circle" size={28} />
					</div>
					<h1 class="text-2xl font-black text-primary">Your spot is paused</h1>
					<p class="text-sm text-muted">
						Your turn was passed, but you haven't lost your place entirely.
					</p>

					<div class="pt-2">
						<span class="text-sm text-muted font-bold block">{currentEntry.shop_name}</span>
						<span class="text-xs text-dim mt-1 block"
							>Show this screen at the counter and staff will put you back in line.</span
						>
					</div>
				</div>
			{:else if currentEntry.presence_state === 'arrived'}
				<!-- STATE 3 — Arrived -->
				<div
					class="text-center py-8 space-y-4 bg-canvas/40 border border-white/[0.03] rounded-3xl p-6"
				>
					<div class="mx-auto w-16 h-16 rounded-full bg-system-success/10 border border-system-success/30 flex items-center justify-center text-system-success pop-in">
						<Icon name="check-circle" size={28} />
					</div>
					<h1 class="text-2xl font-black text-primary">You're confirmed!</h1>
					<p class="text-sm text-muted">
						Take a seat — we'll call your token when it's your turn.
					</p>
					<div
						class="pt-2 flex justify-between items-center text-xs text-muted border-t border-white/[0.03] mt-4"
					>
						<span
							>Position ahead: <strong class="text-primary">{currentEntry.position_ahead}</strong
							></span
						>
						<span
							>Est. Wait: <strong class="text-primary"
								>{currentEntry.estimated_wait_minutes} min</strong
							></span
						>
					</div>
				</div>
			{:else if currentEntry.presence_state === 'on_the_way'}
				<!-- STATE 2 — On The Way (PIN / GPS verification) -->
				<div class="space-y-6">
					<!-- Queue info card -->
					<div
						class="bg-canvas/50 border border-white/[0.05] rounded-2xl p-4 flex justify-around text-center"
					>
						<div>
							<div class="text-[10px] text-muted font-bold uppercase tracking-wider">
								Ahead of You
							</div>
							<div class="text-xl font-mono font-bold text-primary mt-0.5">
								{currentEntry.position_ahead}
							</div>
						</div>
						<div class="w-px bg-white/[0.05]"></div>
						<div>
							<div class="text-[10px] text-muted font-bold uppercase tracking-wider">
								Est. Wait
							</div>
							<div class="text-xl font-mono font-bold text-primary mt-0.5">
								{currentEntry.estimated_wait_minutes}m
							</div>
						</div>
					</div>

					<!-- Arrival confirmation form -->
					<div class="space-y-4">
						<h3 class="text-sm font-bold text-primary uppercase tracking-wider text-center">
							Confirm you've arrived
						</h3>
						<p class="text-xs text-muted text-center">
							At the shop? Enter the 4-digit PIN from the card on the counter.
						</p>

						<form onsubmit={handleConfirmArrivalPin} class="space-y-3">
							<div>
								<label for="pin-input" class="block text-xs font-semibold text-muted mb-1.5 text-center"
									>Counter PIN</label
								>
								<!-- One real input laid invisibly over six display cells: segmented-PIN
								     feel (auto-advance, active-cell highlight) with paste, backspace,
								     and autofill all behaving like a normal single field. -->
								<div class="pin-cells relative mx-auto max-w-[220px]">
									<div class="grid grid-cols-4 gap-1.5" aria-hidden="true">
										{#each Array(4) as _, i}
											<span
												class="h-12 rounded-xl border flex items-center justify-center text-xl font-mono font-bold
													{i < pinInput.length
														? 'border-gold-accent/40 bg-gold-accent/5 text-primary'
														: i === pinInput.length && pinAttemptsRemaining !== 0
															? 'border-gold-accent bg-canvas text-dim pin-cell-active'
															: 'border-white/[0.06] bg-canvas text-dim'}"
											>
												{pinInput[i] ?? ''}
											</span>
										{/each}
									</div>
									<!-- text-base (16px) prevents iOS auto-zoom on focus -->
									<input
										type="tel"
										id="pin-input"
										inputmode="numeric"
										autocomplete="one-time-code"
										pattern="[0-9]*"
										minlength="4"
										maxlength="4"
										class="absolute inset-0 w-full h-full opacity-0 text-base cursor-pointer disabled:cursor-not-allowed"
										bind:value={pinInput}
										disabled={pinAttemptsRemaining === 0 || isSubmitting}
									/>
								</div>
								<button
									type="submit"
									class="w-full mt-3 py-3 bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] disabled:opacity-40 disabled:hover:brightness-100 text-canvas font-bold text-sm rounded-xl cursor-pointer transition-all min-h-[48px]"
									disabled={pinInput.length < 4 || pinAttemptsRemaining === 0 || isSubmitting}
								>
									{isSubmitting ? 'Checking the PIN…' : 'Confirm Arrival'}
								</button>
							</div>

							{#if pinError}
								<p
									class="text-xs text-system-error mt-1 text-center font-medium bg-system-error/10 border border-system-error/20 rounded-lg py-2 px-3"
								>
									{pinError}
								</p>
							{/if}

							{#if pinAttemptsRemaining === 0}
								<p
									class="text-xs text-system-error mt-1 text-center font-bold bg-system-error/10 border border-system-error/30 rounded-lg py-2 px-3"
								>
									Too many attempts. Ask staff to confirm your arrival.
								</p>
							{/if}
						</form>

						<div class="relative flex py-2 items-center">
							<div class="flex-grow border-t border-white/[0.05]"></div>
							<span
								class="flex-shrink mx-4 text-xs font-bold text-dim uppercase tracking-widest"
								>or</span
							>
							<div class="flex-grow border-t border-white/[0.05]"></div>
						</div>

						<!-- GPS Option -->
						<div class="space-y-2">
							<button
								type="button"
								class="w-full py-3 bg-surface hover:bg-titanium active:bg-matte border border-white/[0.06] disabled:opacity-50 text-primary font-bold text-xs rounded-xl cursor-pointer transition-colors flex items-center justify-center space-x-2 min-h-[48px]"
								onclick={handleConfirmArrivalGps}
								disabled={gpsLoading || isSubmitting}
							>
								{#if gpsLoading}
									<span class="inline-block w-3.5 h-3.5 border-2 border-white/20 border-t-gold-accent rounded-full animate-spin motion-reduce:animate-none"></span>
									<span>Checking your location…</span>
								{:else}
									<Icon name="map-pin" size={14} />
									<span>Confirm with GPS instead</span>
								{/if}
							</button>

							{#if gpsError}
								<p
									class="text-xs text-system-error text-center font-medium bg-system-error/10 border border-system-error/20 rounded-lg py-2 px-3"
								>
									{gpsError}
								</p>
							{/if}
						</div>
					</div>
				</div>
			{:else}
				<!-- STATE 1 — Remote / Notified -->
				<div class="space-y-6">
					<!-- Queue info card -->
					<div
						class="bg-canvas/50 border border-white/[0.05] rounded-2xl p-4 flex justify-around text-center"
					>
						<div>
							<div class="text-[10px] text-muted font-bold uppercase tracking-wider">
								Ahead of You
							</div>
							<div class="text-xl font-mono font-bold text-primary mt-0.5">
								{currentEntry.position_ahead}
							</div>
						</div>
						<div class="w-px bg-white/[0.05]"></div>
						<div>
							<div class="text-[10px] text-muted font-bold uppercase tracking-wider">
								Est. Wait
							</div>
							<div class="text-xl font-mono font-bold text-primary mt-0.5">
								{currentEntry.estimated_wait_minutes}m
							</div>
						</div>
					</div>

					<!-- Services list -->
					<div class="space-y-2">
						<span class="text-xs font-bold text-muted uppercase tracking-wider block"
							>Requested Services</span
						>
						<div
							class="bg-canvas/30 border border-white/[0.05] rounded-2xl p-4 divide-y divide-white/[0.04]"
						>
							{#each currentEntry.services as svc}
								<div class="flex justify-between items-center py-2 first:pt-0 last:pb-0 text-xs">
									<span class="font-extrabold text-primary">{svc.name}</span>
									<span class="text-muted">{svc.duration_minutes} min</span>
								</div>
							{/each}
						</div>
					</div>

					<p class="text-xs text-muted text-center">
						We'll notify you when it's almost your turn — no need to do anything right now.
					</p>

					<!-- Action Buttons -->
					<div class="space-y-3 pt-2">
						<p class="text-xs text-dim text-center" id="on-the-way-hint">
							Tap this once you've left for the shop:
						</p>
						<button
							aria-describedby="on-the-way-hint"
							type="button"
							class="w-full py-4 bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] disabled:opacity-40 disabled:hover:brightness-100 text-canvas font-black text-sm rounded-xl cursor-pointer transition-all shadow-[0_0_20px_rgba(200,169,107,0.15)] flex items-center justify-center space-x-1 min-h-[48px]"
							onclick={handleOnTheWay}
							disabled={isSubmitting}
						>
							{#if isSubmitting}
								<span>Letting the shop know…</span>
							{:else}
								<span>I'm On My Way</span>
								<Icon name="arrow-right" size={16} />
							{/if}
						</button>

						<button
							type="button"
							class="w-full py-3 bg-canvas/40 hover:bg-surface/40 active:bg-matte/40 border border-white/[0.03] text-muted hover:text-primary active:text-primary font-bold text-sm rounded-xl cursor-pointer transition-colors min-h-[48px]"
							onclick={handleCancel}
							disabled={isSubmitting}
						>
							Cancel My Spot
						</button>
					</div>

					{#if actionError}
						<p
							class="text-xs text-system-error text-center font-medium bg-system-error/10 border border-system-error/20 rounded-lg py-2 px-3"
						>
							{actionError}
						</p>
					{/if}
				</div>
			{/if}
			</div>
			{/key}
		{:else}
			<div class="text-center py-10 space-y-4">
				<span class="inline-block w-8 h-8 border-2 border-white/10 border-t-gold-accent/60 rounded-full animate-spin motion-reduce:animate-none mx-auto"></span>
				<p class="text-sm text-muted font-medium">Loading your spot…</p>
			</div>
		{/if}
	</div>
</div>

<style>
	:global(.animate-float-slow) {
		animation: float 3s ease-in-out infinite;
	}

	@keyframes float {
		0%, 100% {
			transform: translateY(0);
		}
		50% {
			transform: translateY(-6px);
		}
	}

	/* Soft rise on every state change (wrapped in {#key}) */
	.state-enter {
		animation: state-enter 0.22s cubic-bezier(0.16, 1, 0.3, 1);
	}

	@keyframes state-enter {
		from {
			opacity: 0;
			transform: translateY(8px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}

	/* One-time check pop on arrival confirmation */
	.pop-in {
		animation: pop-in 0.3s cubic-bezier(0.16, 1, 0.3, 1);
	}

	@keyframes pop-in {
		from {
			transform: scale(0.6);
			opacity: 0;
		}
		to {
			transform: scale(1);
			opacity: 1;
		}
	}

	/* Peak-end: a single expanding gold ring behind the completed icon */
	.halo-once::after {
		content: '';
		position: absolute;
		inset: 0;
		border-radius: 999px;
		border: 1px solid rgba(200, 169, 107, 0.6);
		animation: halo 1s cubic-bezier(0.16, 1, 0.3, 1) 0.15s both;
		pointer-events: none;
	}

	@keyframes halo {
		from {
			transform: scale(1);
			opacity: 1;
		}
		to {
			transform: scale(2.2);
			opacity: 0;
		}
	}

	/* Active PIN cell: soft breathing border while it waits for the next digit */
	.pin-cell-active {
		animation: cell-breathe 1.2s ease-in-out infinite;
	}

	@keyframes cell-breathe {
		0%, 100% {
			border-color: rgba(200, 169, 107, 1);
		}
		50% {
			border-color: rgba(200, 169, 107, 0.35);
		}
	}

	@media (prefers-reduced-motion: reduce) {
		:global(.animate-float-slow),
		.state-enter,
		.pop-in,
		.halo-once::after,
		.pin-cell-active {
			animation: none;
		}
	}
</style>
