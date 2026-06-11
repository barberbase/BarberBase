<script lang="ts">
	import { onMount } from 'svelte';
	import { getApiBase } from '$lib/api/client';

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
		const url = `${apiBase}/v1/stream/${locationId}?t=${encodeURIComponent(token)}`;
		let es: EventSource | null = null;
		let reconnectDelay = 1000;
		let idleTimer: ReturnType<typeof setTimeout>;
		let stopped = false;

		const resetIdle = () => {
			clearTimeout(idleTimer);
			idleTimer = setTimeout(() => {
				es?.close();
				stopped = true;
			}, 4 * 60 * 60 * 1000); // 4 hours idle timeout
		};

		const refetch = async () => {
			try {
				const apiBaseUrl = getClientApiBase();
				const res = await fetch(`${apiBaseUrl}/v1/queue/my-status`, {
					headers: { 'X-Session-Token': token }
				});
				if (res.ok) {
					const freshData = await res.json();
					currentEntry = freshData;
					localQueueVersion = freshData.queue_version ?? localQueueVersion;
					resetIdle();
				}
			} catch (err) {
				console.error('[SSE Client] Refetch failed:', err);
			}
		};

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

			es.addEventListener('queue_changed', (e) => {
				try {
					const eventData = JSON.parse(e.data);
					if (eventData.queue_version > localQueueVersion) {
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

<div class="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-zinc-950 text-slate-100 flex flex-col items-center justify-center p-4 md:p-6 font-sans">
	<div class="w-full max-w-md bg-slate-900/60 backdrop-blur-xl border border-slate-800/80 rounded-3xl p-6 shadow-2xl space-y-6 relative overflow-hidden">
		<!-- Subtle ambient backdrop light glow -->
		<div class="absolute -top-24 -left-24 w-48 h-48 bg-amber-500/10 rounded-full blur-3xl pointer-events-none"></div>
		<div class="absolute -bottom-24 -right-24 w-48 h-48 bg-amber-500/5 rounded-full blur-3xl pointer-events-none"></div>

		<!-- ERROR PAGES -->
		{#if initialError === 'invalid_link'}
			<div class="text-center py-10 space-y-4">
				<div class="text-5xl">⚠️</div>
				<h1 class="text-xl font-extrabold text-slate-200">Invalid Link</h1>
				<p class="text-sm text-slate-400">This link is not valid. Please request a new one via WhatsApp.</p>
			</div>
		{:else if initialError === 'expired'}
			<div class="text-center py-10 space-y-4">
				<div class="text-5xl">⏰</div>
				<h1 class="text-xl font-extrabold text-slate-200">Link Expired</h1>
				<p class="text-sm text-slate-400">Your session has expired (links are valid for 23 hours).</p>
			</div>
		{:else if initialError === 'not_found'}
			<div class="text-center py-10 space-y-4">
				<div class="text-5xl">📭</div>
				<h1 class="text-xl font-extrabold text-slate-200">Inactive Entry</h1>
				<p class="text-sm text-slate-400">This queue entry is no longer active.</p>
			</div>
		{:else if currentEntry}
			<!-- HEADER SECTION -->
			<div class="flex justify-between items-center border-b border-slate-800/80 pb-4">
				<div>
					<h2 class="text-xs font-bold text-amber-500 uppercase tracking-widest">{currentEntry.shop_name || 'BarberBase'}</h2>
					<p class="text-[10px] text-slate-400 mt-0.5">{currentEntry.location_name || 'Salon Location'}</p>
				</div>
				<span class="bg-amber-500/10 border border-amber-500/20 text-amber-400 text-xs font-black px-3.5 py-1.5 rounded-full">
					Token #{currentEntry.token_number}
				</span>
			</div>

			<!-- NORMAL STATES CONTROLLER -->
			{#if currentEntry.state === 'completed'}
				<!-- STATE 6 — Completed -->
				<div class="text-center py-6 space-y-4">
					<div class="text-5xl animate-bounce-slow">🎉</div>
					<h1 class="text-2xl font-black text-slate-100">All Done!</h1>
					<p class="text-sm text-slate-400">Thanks for visiting {currentEntry.shop_name || 'us'}.</p>
					
					<!-- Feedback Star Widget -->
					<div class="bg-slate-950/40 border border-slate-800/60 rounded-2xl p-5 space-y-3.5 mt-2">
						<span class="text-xs font-bold text-slate-400 uppercase tracking-wider block">Rate your experience</span>
						<div class="flex justify-center space-x-2.5">
							{#each [1, 2, 3, 4, 5] as star}
								<button
									type="button"
									class="text-3xl focus:outline-none transition-transform active:scale-125 duration-100 min-h-[48px] min-w-[48px] flex items-center justify-center cursor-pointer"
									onclick={() => handleFeedback(star)}
									disabled={feedbackSubmitted}
								>
									<span class={star <= (feedbackRating || 0) ? 'text-amber-400' : 'text-slate-700'}>★</span>
								</button>
							{/each}
						</div>
						{#if feedbackMessage}
							<p class="text-xs font-semibold text-amber-400 mt-2">{feedbackMessage}</p>
						{/if}
					</div>
				</div>
			{:else if currentEntry.state === 'called'}
				<!-- STATE 4 — Called -->
				<div class="text-center py-8 space-y-4 bg-amber-500/10 border border-amber-500/30 rounded-3xl p-6 ring-2 ring-amber-500/20">
					<div class="text-5xl animate-pulse">🔔</div>
					<h1 class="text-2xl font-black text-amber-400">It's Your Turn!</h1>
					<p class="text-sm text-amber-200">Please go to the barber chair now.</p>
				</div>
			{:else if currentEntry.state === 'in_progress'}
				<!-- STATE 5 — In Progress -->
				<div class="text-center py-8 space-y-4 bg-emerald-500/10 border border-emerald-500/30 rounded-3xl p-6">
					<div class="text-5xl">✂️</div>
					<h1 class="text-2xl font-black text-emerald-400">In Progress</h1>
					<p class="text-sm text-emerald-200">Enjoy your service!</p>
				</div>
			{:else if currentEntry.presence_state === 'snoozed' || currentEntry.state === 'skipped' || currentEntry.state === 'no_show'}
				<!-- STATE 7 — Spot Paused -->
				<div class="text-center py-6 space-y-4">
					<div class="text-5xl">⏸</div>
					<h1 class="text-2xl font-black text-slate-300">Spot Paused</h1>
					<p class="text-sm text-slate-400">Your turn was passed. Ask staff to reactivate your spot.</p>
					
					<div class="pt-2">
						<span class="text-sm text-slate-400 font-bold block">{currentEntry.shop_name}</span>
						<span class="text-xs text-slate-500 mt-1 block">Please consult our front desk team.</span>
					</div>
				</div>
			{:else if currentEntry.presence_state === 'arrived'}
				<!-- STATE 3 — Arrived -->
				<div class="text-center py-8 space-y-4 bg-slate-950/40 border border-slate-800 rounded-3xl p-6">
					<div class="text-5xl">✅</div>
					<h1 class="text-2xl font-black text-slate-200">You're Confirmed!</h1>
					<p class="text-sm text-slate-400">Please wait inside the shop. We will call you when it is your turn.</p>
					<div class="pt-2 flex justify-between items-center text-xs text-slate-500 border-t border-slate-800/60 mt-4">
						<span>Position ahead: <strong class="text-slate-300">{currentEntry.position_ahead}</strong></span>
						<span>Est. Wait: <strong class="text-slate-300">{currentEntry.estimated_wait_minutes} min</strong></span>
					</div>
				</div>
			{:else if currentEntry.presence_state === 'on_the_way'}
				<!-- STATE 2 — On The Way (PIN / GPS verification) -->
				<div class="space-y-6">
					<!-- Queue info card -->
					<div class="bg-slate-950/50 border border-slate-850 rounded-2xl p-4 flex justify-around text-center">
						<div>
							<div class="text-[10px] text-slate-500 font-bold uppercase tracking-wider">Ahead of You</div>
							<div class="text-xl font-black text-slate-200 mt-0.5">{currentEntry.position_ahead}</div>
						</div>
						<div class="w-px bg-slate-850"></div>
						<div>
							<div class="text-[10px] text-slate-500 font-bold uppercase tracking-wider">Est. Wait</div>
							<div class="text-xl font-black text-slate-200 mt-0.5">{currentEntry.estimated_wait_minutes}m</div>
						</div>
					</div>

					<!-- Arrival confirmation form -->
					<div class="space-y-4">
						<h3 class="text-sm font-bold text-slate-200 uppercase tracking-wider text-center">Verify Physical Arrival</h3>

						<form onsubmit={handleConfirmArrivalPin} class="space-y-3">
							<div>
								<label for="pin-input" class="block text-xs font-semibold text-slate-400 mb-1.5">Enter 4-Digit Counter PIN</label>
								<div class="flex gap-2">
									<input
										type="tel"
										id="pin-input"
										inputmode="numeric"
										maxlength="6"
										placeholder="PIN on counter card"
										class="flex-1 bg-slate-950 border border-slate-800 rounded-xl px-4 py-3 text-sm focus:outline-none focus:border-amber-500 focus:ring-1 focus:ring-amber-500/50 placeholder:text-slate-650 min-h-[48px]"
										bind:value={pinInput}
										disabled={pinAttemptsRemaining === 0 || isSubmitting}
									/>
									<button
										type="submit"
										class="px-5 bg-amber-500 hover:bg-amber-400 active:bg-amber-600 disabled:opacity-40 disabled:hover:bg-amber-500 text-slate-950 font-extrabold text-sm rounded-xl cursor-pointer transition-colors min-h-[48px]"
										disabled={!pinInput || pinAttemptsRemaining === 0 || isSubmitting}
									>
										Confirm
									</button>
								</div>
							</div>

							{#if pinError}
								<p class="text-xs text-rose-400 mt-1 text-center font-medium bg-rose-950/20 border border-rose-900/40 rounded-lg py-2 px-3">{pinError}</p>
							{/if}

							{#if pinAttemptsRemaining === 0}
								<p class="text-xs text-rose-400 mt-1 text-center font-bold bg-rose-950/30 border border-rose-900/60 rounded-lg py-2 px-3">
									Too many attempts. Ask staff to confirm your arrival.
								</p>
							{/if}
						</form>

						<div class="relative flex py-2 items-center">
							<div class="flex-grow border-t border-slate-850"></div>
							<span class="flex-shrink mx-4 text-xs font-bold text-slate-500 uppercase tracking-widest">or</span>
							<div class="flex-grow border-t border-slate-850"></div>
						</div>

						<!-- GPS Option -->
						<div class="space-y-2">
							<button
								type="button"
								class="w-full py-3 bg-slate-800 hover:bg-slate-700 active:bg-slate-750 border border-slate-750 disabled:opacity-50 text-slate-200 font-bold text-xs rounded-xl cursor-pointer transition-colors flex items-center justify-center space-x-2 min-h-[48px]"
								onclick={handleConfirmArrivalGps}
								disabled={gpsLoading || isSubmitting}
							>
								{#if gpsLoading}
									<span class="animate-spin text-xs">⏳</span>
									<span>Retrieving GPS...</span>
								{:else}
									<span>📍 Auto-Confirm using GPS</span>
								{/if}
							</button>

							{#if gpsError}
								<p class="text-xs text-rose-400 text-center font-medium bg-rose-950/20 border border-rose-900/40 rounded-lg py-2 px-3">{gpsError}</p>
							{/if}
						</div>
					</div>
				</div>
			{:else}
				<!-- STATE 1 — Remote / Notified -->
				<div class="space-y-6">
					<!-- Queue info card -->
					<div class="bg-slate-950/50 border border-slate-850 rounded-2xl p-4 flex justify-around text-center">
						<div>
							<div class="text-[10px] text-slate-500 font-bold uppercase tracking-wider">Ahead of You</div>
							<div class="text-xl font-black text-slate-200 mt-0.5">{currentEntry.position_ahead}</div>
						</div>
						<div class="w-px bg-slate-850"></div>
						<div>
							<div class="text-[10px] text-slate-500 font-bold uppercase tracking-wider">Est. Wait</div>
							<div class="text-xl font-black text-slate-200 mt-0.5">{currentEntry.estimated_wait_minutes}m</div>
						</div>
					</div>

					<!-- Services list -->
					<div class="space-y-2">
						<span class="text-xs font-bold text-slate-400 uppercase tracking-wider block">Requested Services</span>
						<div class="bg-slate-950/30 border border-slate-850 rounded-2xl p-4 divide-y divide-slate-850/60">
							{#each currentEntry.services as svc}
								<div class="flex justify-between items-center py-2 first:pt-0 last:pb-0 text-xs">
									<span class="font-extrabold text-slate-200">{svc.name}</span>
									<span class="text-slate-400">{svc.duration_minutes} min</span>
								</div>
							{/each}
						</div>
					</div>

					<!-- Action Buttons -->
					<div class="space-y-3 pt-2">
						<button
							type="button"
							class="w-full py-4 bg-amber-500 hover:bg-amber-400 active:bg-amber-600 disabled:opacity-40 disabled:hover:bg-amber-500 text-slate-950 font-black text-sm rounded-xl cursor-pointer transition-all shadow-lg flex items-center justify-center space-x-1 min-h-[48px]"
							onclick={handleOnTheWay}
							disabled={isSubmitting}
						>
							<span>I'm On My Way</span>
							<span class="text-xs font-bold">🏃</span>
						</button>

						<button
							type="button"
							class="w-full py-3 bg-slate-950/40 hover:bg-slate-800/40 active:bg-slate-900/40 border border-slate-800 text-slate-400 hover:text-slate-300 font-bold text-xs rounded-xl cursor-pointer transition-colors min-h-[48px]"
							onclick={handleCancel}
							disabled={isSubmitting}
						>
							Cancel My Spot
						</button>
					</div>

					{#if actionError}
						<p class="text-xs text-rose-400 text-center font-medium bg-rose-950/20 border border-rose-900/40 rounded-lg py-2 px-3">{actionError}</p>
					{/if}
				</div>
			{/if}
		{:else}
			<div class="text-center py-10 space-y-4">
				<span class="animate-spin text-4xl block">⏳</span>
				<p class="text-sm text-slate-400 font-medium">Fetching status details...</p>
			</div>
		{/if}
	</div>
</div>

<style>
	:global(.animate-bounce-slow) {
		animation: bounce 2s infinite;
	}

	@keyframes bounce {
		0%, 100% {
			transform: translateY(-5%);
			animation-timing-function: cubic-bezier(0.8,0,1,1);
		}
		50% {
			transform: none;
			animation-timing-function: cubic-bezier(0,0,0.2,1);
		}
	}
</style>
