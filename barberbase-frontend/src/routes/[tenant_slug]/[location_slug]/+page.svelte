<script lang="ts">
	import { replaceState } from '$app/navigation';
	import { page } from '$app/stores';

	let { data }: { data: any } = $props();

	// Active selected variants
	let selectedVariantIds = $state<string[]>(data.variantIds || []);
	let bookingOptions = $state<any>(data.initialBookingOptions);
	let isResolving = $state<boolean>(false);
	let resolveError = $state<string | null>(null);
	let debounceTimer: any = null;

	// Categories & gender tabs
	const categories = (data.catalog?.categories || []) as any[];
	const genders = Array.from(new Set(categories.map((c: any) => c.gender))).filter(
		Boolean
	) as string[];
	let activeTab = $state<string>(genders[0] || 'men');

	// Derived categories matching active tab to avoid HTML template implicit any errors
	let filteredCategories = $derived(categories.filter((c: any) => c.gender === activeTab));

	// Cloudflare Turnstile state
	let turnstileToken = $state<string | null>(null);
	let turnstileWidgetId = $state<any>(null);
	let isJoining = $state<boolean>(false);
	let joinError = $state<string | null>(null);
	let checkinResponse = $state<any>(null);

	// Fetch booking options from the API
	async function resolveOptions() {
		if (selectedVariantIds.length === 0) {
			bookingOptions = null;
			resolveError = null;
			return;
		}

		isResolving = true;
		resolveError = null;
		try {
			const res = await fetch(
				`${data.apiBase}/v1/public/locations/${data.location.id}/booking-options`,
				{
					method: 'POST',
					headers: {
						'Content-Type': 'application/json'
					},
					body: JSON.stringify({
						variant_ids: selectedVariantIds,
						party_size: 1
					})
				}
			);
			if (res.ok) {
				bookingOptions = await res.json();
			} else {
				bookingOptions = { error: 'Unable to load options, please retry' };
				resolveError = 'Unable to load options, please retry';
			}
		} catch (err) {
			bookingOptions = { error: 'Unable to load options, please retry' };
			resolveError = 'Unable to load options, please retry';
		} finally {
			isResolving = false;
		}
	}

	function triggerResolveOptions() {
		if (debounceTimer) clearTimeout(debounceTimer);
		debounceTimer = setTimeout(resolveOptions, 300);
	}

	// Toggle variant selection
	function toggleVariant(variantId: string) {
		if (selectedVariantIds.includes(variantId)) {
			selectedVariantIds = selectedVariantIds.filter((id) => id !== variantId);
		} else {
			selectedVariantIds = [...selectedVariantIds, variantId];
		}

		// Update URL params
		const newUrl = new URL(window.location.href);
		if (selectedVariantIds.length > 0) {
			newUrl.searchParams.set('v', selectedVariantIds.join(','));
		} else {
			newUrl.searchParams.delete('v');
		}
		replaceState(newUrl.pathname + newUrl.search, {});

		triggerResolveOptions();
	}

	// Cloudflare Turnstile integration
	function onTurnstileCallback(token: string) {
		turnstileToken = token;
	}

	function initTurnstile() {
		if (typeof window !== 'undefined' && (window as any).turnstile) {
			if (turnstileWidgetId) {
				try {
					(window as any).turnstile.remove(turnstileWidgetId);
				} catch (_) {}
			}
			const container = document.getElementById('turnstile-container');
			if (container) {
				turnstileWidgetId = (window as any).turnstile.render('#turnstile-container', {
					sitekey: import.meta.env.PUBLIC_TURNSTILE_SITE_KEY || '1x00000000000000000000AA',
					callback: onTurnstileCallback,
					theme: 'dark'
				});
			}
		}
	}

	function resetTurnstile() {
		turnstileToken = null;
		if (turnstileWidgetId && typeof window !== 'undefined' && (window as any).turnstile) {
			(window as any).turnstile.reset(turnstileWidgetId);
		}
	}

	let turnstileInterval: any;
	$effect(() => {
		if (typeof window !== 'undefined') {
			(window as any).onTurnstileSuccess = onTurnstileCallback;

			turnstileInterval = setInterval(() => {
				if ((window as any).turnstile) {
					clearInterval(turnstileInterval);
					initTurnstile();
				}
			}, 100);
		}

		return () => {
			if (turnstileInterval) clearInterval(turnstileInterval);
			if (typeof window !== 'undefined') {
				delete (window as any).onTurnstileSuccess;
				if (turnstileWidgetId && (window as any).turnstile) {
					try {
						(window as any).turnstile.remove(turnstileWidgetId);
					} catch (_) {}
				}
			}
		};
	});

	// Join Queue CTA
	async function handleJoin() {
		if (!turnstileToken || selectedVariantIds.length === 0) return;
		isJoining = true;
		joinError = null;
		try {
			const res = await fetch(
				`${data.apiBase}/v1/public/locations/${data.location.id}/checkin-intents`,
				{
					method: 'POST',
					headers: {
						'Content-Type': 'application/json'
					},
					body: JSON.stringify({
						variant_ids: selectedVariantIds,
						party_size: 1
					})
				}
			);
			if (res.status === 201) {
				checkinResponse = await res.json();
			} else {
				let errData: any = {};
				try {
					errData = await res.json();
				} catch (_) {}
				joinError = errData.message || 'Failed to join queue. Please try again.';
				resetTurnstile();
			}
		} catch (err) {
			joinError = 'Network error. Please try again.';
			resetTurnstile();
		} finally {
			isJoining = false;
		}
	}

	// Human-readable status mapping
	function getStatusLabel(status: string) {
		switch (status) {
			case 'open':
				return 'Open';
			case 'closing_soon':
				return 'Closing Soon';
			case 'temporarily_closed':
				return 'Temporarily Closed';
			case 'closed':
				return 'Closed';
			default:
				return 'Closed';
		}
	}

	// Human-readable blocked reason mapping
	function getBlockedMessage(options: any) {
		if (!options) return '';
		const { blocked_reason, allowed_entry_methods } = options;
		switch (blocked_reason) {
			case 'shop_closed':
				return 'The shop is currently closed.';
			case 'queue_full':
				return 'The queue is currently full. We cannot accept more walk-ins right now.';
			case 'requires_appointment':
				return 'Selected services require an appointment.';
			case 'closing_time_exceeded':
				return 'Estimated service time exceeds closing hours.';
			default:
				if (blocked_reason === null && allowed_entry_methods?.includes('appointment')) {
					return 'Appointment booking coming soon.';
				}
				return blocked_reason || 'Walk-in registration is currently disabled.';
		}
	}
</script>

<svelte:head>
	<title>{data.location.name} | Queue Join</title>
	<meta name="description" content="Join the queue for {data.location.name} via WhatsApp." />
	<script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script>
</svelte:head>

<div class="shop-container">
	<!-- HEADER -->
	<header class="shop-header">
		<div class="shop-info">
			<h1 class="shop-name">{data.location.name}</h1>
			<div class="badges">
				<span class="status-badge {data.location.shop_status}">
					{getStatusLabel(data.location.shop_status)}
				</span>
				{#if data.location.shop_status !== 'closed' && data.location.shop_status !== 'temporarily_closed'}
					<span class="queue-badge">
						{data.location.queue_length} waiting
					</span>
					<span class="wait-badge">
						~{data.location.estimated_wait_minutes} min wait
					</span>
				{/if}
			</div>
		</div>
	</header>

	{#if data.location.shop_status === 'closed'}
		<!-- CLOSED SHOP VIEW -->
		<section class="closed-card">
			<div class="closed-icon">🚪</div>
			<h2>We are currently closed</h2>
			{#if data.location.business_hours_today?.opens_at}
				<p class="hours-info">We open today at {data.location.business_hours_today.opens_at}</p>
			{:else}
				<p class="hours-info">Please check back during our regular business hours.</p>
			{/if}
		</section>
	{:else}
		<!-- SERVICE SELECTOR -->
		<main class="main-content">
			<section class="catalog-section">
				<h2 class="section-title">Select Services</h2>

				<!-- GENDER TABS -->
				{#if genders.length > 1}
					<div class="tabs-container">
						{#each genders as gender}
							<button
								class="tab-btn {activeTab === gender ? 'active' : ''}"
								onclick={() => (activeTab = gender)}
							>
								{gender.charAt(0).toUpperCase() + gender.slice(1)}
							</button>
						{/each}
					</div>
				{/if}

				<!-- CATEGORIES & GROUPS -->
				<div class="catalog-tree">
					{#each filteredCategories as category}
						<div class="category-block">
							<h3 class="category-name">{category.name}</h3>

							{#each category.groups as group}
								<div class="group-block">
									<h4 class="group-name">{group.name}</h4>
									{#if group.description}
										<p class="group-desc">{group.description}</p>
									{/if}

									<div class="variants-grid">
										{#each group.variants as variant}
											<button
												class="variant-card {selectedVariantIds.includes(variant.id)
													? 'selected'
													: ''}"
												onclick={() => toggleVariant(variant.id)}
											>
												<div class="variant-meta">
													<span class="variant-name">{variant.name}</span>
													{#if variant.is_popular}
														<span class="popular-badge">Popular</span>
													{/if}
												</div>
												{#if variant.description}
													<p class="variant-desc">{variant.description}</p>
												{/if}
												<div class="variant-pricing">
													<span class="variant-duration">{variant.duration_minutes} min</span>
													<span class="variant-price">₹{variant.price_paise / 100}</span>
												</div>
											</button>
										{/each}
									</div>
								</div>
							{/each}
						</div>
					{:else}
						<p class="empty-catalog">No services available for this gender category.</p>
					{/each}
				</div>
			</section>

			<!-- BOOKING OPTIONS PANEL -->
			{#if selectedVariantIds.length > 0}
				<section class="booking-panel">
					<h2 class="panel-title">Your Booking</h2>

					{#if isResolving}
						<div class="loader-container">
							<div class="spinner"></div>
							<span>Calculating totals...</span>
						</div>
					{:else if resolveError}
						<div class="inline-error">
							<p>{resolveError}</p>
							<button class="retry-btn" onclick={resolveOptions}>Retry</button>
						</div>
					{:else if bookingOptions}
						<div class="totals-grid">
							<div class="total-card">
								<span class="total-label">Total Price</span>
								<span class="total-val">₹{bookingOptions.total_price_paise / 100}</span>
							</div>
							<div class="total-card">
								<span class="total-label">Total Duration</span>
								<span class="total-val">{bookingOptions.total_duration_minutes} min</span>
							</div>
							<div class="total-card">
								<span class="total-label">Est. Wait Time</span>
								<span class="total-val">{bookingOptions.estimated_wait_minutes} min</span>
							</div>
						</div>

						<!-- ENTRANCE GATES -->
						{#if bookingOptions.allowed_entry_methods?.includes('walk_in')}
							<!-- JOIN CTA FORM -->
							<div class="join-form">
								<div class="turnstile-wrapper">
									<div id="turnstile-container"></div>
								</div>

								{#if joinError}
									<div class="inline-error">{joinError}</div>
								{/if}

								<button
									class="join-btn"
									disabled={!turnstileToken || isJoining}
									onclick={handleJoin}
								>
									{isJoining ? 'Securing Spot...' : 'Join via WhatsApp'}
								</button>
							</div>
						{:else}
							<!-- BLOCKED STATE -->
							<div class="blocked-card">
								<div class="blocked-icon">🔒</div>
								<p class="blocked-message">{getBlockedMessage(bookingOptions)}</p>
							</div>
						{/if}
					{/if}
				</section>
			{/if}
		</main>
	{/if}
</div>

<!-- CONFIRMATION DIALOG/MODAL -->
{#if checkinResponse}
	<div class="modal-overlay">
		<div class="confirmation-card">
			<div class="modal-icon">📱</div>
			<h2>WhatsApp will open</h2>
			<p>
				Simply press <strong>Send</strong> on the pre-filled message inside WhatsApp to confirm your spot
				in the queue.
			</p>
			<a
				href={checkinResponse.deep_link}
				class="wa-confirm-btn"
				target="_blank"
				rel="noopener noreferrer"
			>
				Open WhatsApp & Send
			</a>
		</div>
	</div>
{/if}

<style>
	:global(body) {
		margin: 0;
		font-family:
			'Inter',
			system-ui,
			-apple-system,
			sans-serif;
		background-color: #0b0f19;
		color: #f3f4f6;
	}

	.shop-container {
		max-width: 800px;
		margin: 0 auto;
		padding: 1.5rem 1rem 5rem 1rem;
		min-height: 100vh;
		box-sizing: border-box;
	}

	.shop-header {
		background: rgba(30, 41, 59, 0.45);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
		border: 1px solid rgba(255, 255, 255, 0.08);
		border-radius: 1rem;
		padding: 1.5rem;
		margin-bottom: 2rem;
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.shop-name {
		font-size: 1.75rem;
		font-weight: 800;
		margin: 0 0 0.75rem 0;
		background: linear-gradient(135deg, #a78bfa, #c084fc);
		-webkit-background-clip: text;
		-webkit-text-fill-color: transparent;
	}

	.badges {
		display: flex;
		gap: 0.5rem;
		flex-wrap: wrap;
	}

	.status-badge,
	.queue-badge,
	.wait-badge {
		font-size: 0.8rem;
		font-weight: 600;
		padding: 0.35rem 0.75rem;
		border-radius: 9999px;
		border: 1px solid transparent;
	}

	.status-badge.open {
		background: rgba(16, 185, 129, 0.15);
		color: #34d399;
		border-color: rgba(16, 185, 129, 0.25);
	}

	.status-badge.closing_soon {
		background: rgba(245, 158, 11, 0.15);
		color: #fbbf24;
		border-color: rgba(245, 158, 11, 0.25);
	}

	.status-badge.temporarily_closed,
	.status-badge.closed {
		background: rgba(239, 68, 68, 0.15);
		color: #f87171;
		border-color: rgba(239, 68, 68, 0.25);
	}

	.queue-badge {
		background: rgba(59, 130, 246, 0.15);
		color: #60a5fa;
		border-color: rgba(59, 130, 246, 0.25);
	}

	.wait-badge {
		background: rgba(139, 92, 246, 0.15);
		color: #a78bfa;
		border-color: rgba(139, 92, 246, 0.25);
	}

	/* CLOSED SHOP VIEW */
	.closed-card {
		background: rgba(30, 41, 59, 0.45);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
		border: 1px solid rgba(239, 68, 68, 0.2);
		border-radius: 1rem;
		padding: 3rem 1.5rem;
		text-align: center;
	}

	.closed-icon {
		font-size: 3rem;
		margin-bottom: 1rem;
	}

	.closed-card h2 {
		font-size: 1.5rem;
		font-weight: 700;
		margin: 0 0 0.75rem 0;
		color: #f87171;
	}

	.hours-info {
		color: #9ca3af;
		font-size: 1rem;
		margin: 0;
	}

	/* MAIN CONTENT */
	.main-content {
		display: flex;
		flex-direction: column;
		gap: 2rem;
	}

	.section-title {
		font-size: 1.25rem;
		font-weight: 700;
		margin: 0 0 1rem 0;
		color: #e5e7eb;
	}

	/* TABS */
	.tabs-container {
		display: flex;
		gap: 0.5rem;
		background: rgba(30, 41, 59, 0.3);
		border: 1px solid rgba(255, 255, 255, 0.05);
		padding: 0.25rem;
		border-radius: 0.5rem;
		margin-bottom: 1.5rem;
	}

	.tab-btn {
		flex: 1;
		background: transparent;
		border: none;
		color: #9ca3af;
		padding: 0.65rem;
		font-weight: 600;
		font-size: 0.9rem;
		border-radius: 0.35rem;
		cursor: pointer;
		transition: all 0.2s ease;
	}

	.tab-btn:hover {
		color: #e5e7eb;
	}

	.tab-btn.active {
		background: rgba(139, 92, 246, 0.2);
		color: #c084fc;
		border: 1px solid rgba(139, 92, 246, 0.3);
	}

	/* CATALOG */
	.catalog-tree {
		display: flex;
		flex-direction: column;
		gap: 2rem;
	}

	.category-block {
		background: rgba(30, 41, 59, 0.2);
		border: 1px solid rgba(255, 255, 255, 0.04);
		border-radius: 0.75rem;
		padding: 1.25rem;
	}

	.category-name {
		font-size: 1.15rem;
		font-weight: 700;
		margin: 0 0 1.25rem 0;
		color: #f3f4f6;
		border-bottom: 1px solid rgba(255, 255, 255, 0.08);
		padding-bottom: 0.5rem;
	}

	.group-block {
		margin-bottom: 1.5rem;
	}

	.group-block:last-child {
		margin-bottom: 0;
	}

	.group-name {
		font-size: 1rem;
		font-weight: 600;
		margin: 0 0 0.25rem 0;
		color: #e5e7eb;
	}

	.group-desc {
		font-size: 0.85rem;
		color: #9ca3af;
		margin: 0 0 0.85rem 0;
	}

	.variants-grid {
		display: grid;
		grid-template-columns: 1fr;
		gap: 0.75rem;
	}

	@media (min-width: 640px) {
		.variants-grid {
			grid-template-columns: 1fr 1fr;
		}
	}

	.variant-card {
		background: rgba(30, 41, 59, 0.4);
		border: 1px solid rgba(255, 255, 255, 0.06);
		border-radius: 0.5rem;
		padding: 1rem;
		text-align: left;
		cursor: pointer;
		transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
		display: flex;
		flex-direction: column;
		justify-content: space-between;
		gap: 0.75rem;
		width: 100%;
		box-sizing: border-box;
	}

	.variant-card:hover {
		transform: translateY(-2px);
		border-color: rgba(139, 92, 246, 0.4);
		background: rgba(30, 41, 59, 0.65);
	}

	.variant-card.selected {
		background: rgba(139, 92, 246, 0.12);
		border-color: #a78bfa;
		box-shadow: 0 0 15px rgba(139, 92, 246, 0.15);
	}

	.variant-meta {
		display: flex;
		justify-content: space-between;
		align-items: flex-start;
		gap: 0.5rem;
	}

	.variant-name {
		font-weight: 700;
		font-size: 0.95rem;
		color: #f3f4f6;
	}

	.popular-badge {
		background: rgba(245, 158, 11, 0.15);
		color: #fbbf24;
		border: 1px solid rgba(245, 158, 11, 0.3);
		font-size: 0.7rem;
		font-weight: 700;
		padding: 0.15rem 0.4rem;
		border-radius: 0.25rem;
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.variant-desc {
		font-size: 0.8rem;
		color: #9ca3af;
		margin: 0;
		line-height: 1.35;
	}

	.variant-pricing {
		display: flex;
		justify-content: space-between;
		font-size: 0.85rem;
		margin-top: auto;
		border-top: 1px solid rgba(255, 255, 255, 0.04);
		padding-top: 0.5rem;
	}

	.variant-duration {
		color: #9ca3af;
	}

	.variant-price {
		font-weight: 700;
		color: #c084fc;
	}

	/* BOOKING OPTIONS PANEL */
	.booking-panel {
		background: rgba(30, 41, 59, 0.5);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
		border: 1px solid rgba(255, 255, 255, 0.08);
		border-radius: 1rem;
		padding: 1.5rem;
		box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.3);
	}

	.panel-title {
		font-size: 1.25rem;
		font-weight: 700;
		margin: 0 0 1.25rem 0;
		color: #f3f4f6;
	}

	.loader-container {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 0.75rem;
		padding: 1.5rem 0;
		color: #9ca3af;
		font-size: 0.9rem;
	}

	.spinner {
		width: 1.25rem;
		height: 1.25rem;
		border: 2px solid rgba(255, 255, 255, 0.1);
		border-top-color: #a78bfa;
		border-radius: 50%;
		animation: spin 0.8s linear infinite;
	}

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}

	.totals-grid {
		display: grid;
		grid-template-columns: 1fr;
		gap: 0.75rem;
		margin-bottom: 1.5rem;
	}

	@media (min-width: 480px) {
		.totals-grid {
			grid-template-columns: repeat(3, 1fr);
		}
	}

	.total-card {
		background: rgba(15, 23, 42, 0.3);
		border: 1px solid rgba(255, 255, 255, 0.05);
		padding: 0.85rem;
		border-radius: 0.5rem;
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}

	.total-label {
		font-size: 0.75rem;
		color: #9ca3af;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		font-weight: 500;
	}

	.total-val {
		font-size: 1.15rem;
		font-weight: 800;
		color: #f3f4f6;
	}

	/* CTA JOIN FORM */
	.join-form {
		display: flex;
		flex-direction: column;
		gap: 1rem;
	}

	.turnstile-wrapper {
		display: flex;
		justify-content: center;
		background: rgba(15, 23, 42, 0.25);
		padding: 0.75rem;
		border-radius: 0.5rem;
		border: 1px solid rgba(255, 255, 255, 0.04);
	}

	.join-btn {
		width: 100%;
		background: linear-gradient(135deg, #7c3aed, #4f46e5);
		color: #ffffff;
		border: none;
		border-radius: 0.5rem;
		padding: 0.95rem;
		font-weight: 700;
		font-size: 1rem;
		cursor: pointer;
		transition: all 0.2s ease;
		display: flex;
		justify-content: center;
		align-items: center;
	}

	.join-btn:hover:not(:disabled) {
		opacity: 0.95;
		transform: translateY(-1px);
		box-shadow: 0 4px 12px rgba(124, 58, 237, 0.25);
	}

	.join-btn:disabled {
		background: #1e293b;
		color: #64748b;
		cursor: not-allowed;
		border: 1px solid rgba(255, 255, 255, 0.04);
	}

	/* BLOCKED STATE */
	.blocked-card {
		background: rgba(239, 68, 68, 0.05);
		border: 1px solid rgba(239, 68, 68, 0.15);
		border-radius: 0.5rem;
		padding: 1rem;
		display: flex;
		align-items: center;
		gap: 0.75rem;
		color: #f87171;
	}

	.blocked-icon {
		font-size: 1.25rem;
	}

	.blocked-message {
		font-size: 0.9rem;
		font-weight: 600;
		margin: 0;
	}

	.inline-error {
		background: rgba(239, 68, 68, 0.08);
		border: 1px solid rgba(239, 68, 68, 0.2);
		border-radius: 0.5rem;
		padding: 0.85rem;
		color: #f87171;
		font-size: 0.85rem;
		font-weight: 600;
		text-align: center;
	}

	.retry-btn {
		background: #ef4444;
		color: white;
		border: none;
		border-radius: 0.35rem;
		padding: 0.4rem 0.85rem;
		font-size: 0.8rem;
		font-weight: 700;
		cursor: pointer;
		margin-top: 0.5rem;
	}

	/* CONFIRMATION OVERLAY */
	.modal-overlay {
		position: fixed;
		top: 0;
		left: 0;
		right: 0;
		bottom: 0;
		background: rgba(3, 7, 18, 0.85);
		backdrop-filter: blur(8px);
		-webkit-backdrop-filter: blur(8px);
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 1rem;
		z-index: 100;
	}

	.confirmation-card {
		background: #1e293b;
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 1.25rem;
		padding: 2.25rem;
		max-width: 440px;
		width: 100%;
		text-align: center;
		box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
		box-sizing: border-box;
	}

	.modal-icon {
		font-size: 3rem;
		margin-bottom: 1.25rem;
	}

	.confirmation-card h2 {
		font-size: 1.45rem;
		font-weight: 800;
		margin: 0 0 0.85rem 0;
		background: linear-gradient(135deg, #38bdf8, #818cf8);
		-webkit-background-clip: text;
		-webkit-text-fill-color: transparent;
	}

	.confirmation-card p {
		color: #9ca3af;
		font-size: 0.95rem;
		line-height: 1.5;
		margin: 0 0 1.75rem 0;
	}

	.wa-confirm-btn {
		display: inline-block;
		width: 100%;
		background: #25d366;
		color: #0b0f19;
		font-weight: 800;
		font-size: 1rem;
		text-decoration: none;
		border-radius: 0.5rem;
		padding: 0.95rem;
		box-sizing: border-box;
		transition: all 0.2s ease;
		box-shadow: 0 4px 15px rgba(37, 211, 102, 0.2);
	}

	.wa-confirm-btn:hover {
		opacity: 0.95;
		transform: translateY(-1px);
		box-shadow: 0 6px 20px rgba(37, 211, 102, 0.35);
	}
</style>
