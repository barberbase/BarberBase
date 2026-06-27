<script lang="ts">
	import { onMount } from 'svelte';
	import type { QueueEntryStaff, QueueStore } from '$lib/stores/queue.svelte';

	let { entry, store, onClose } = $props<{
		entry: QueueEntryStaff;
		store: QueueStore;
		onClose: () => void;
	}>();

	let mounted = $state(false);
	onMount(() => {
		mounted = true;
	});

	// Calculate subtotal from services
	const subtotalPaise = $derived(
		entry.services.reduce((acc: number, s: any) => acc + (s.price_paise || 0), 0)
	);

	// Discount states
	let discountAmountINR = $state<number>(0);
	let discountReason = $state<string>('');

	// Derived discount in paise
	const discountAmountPaise = $derived(
		Math.max(0, Math.min(subtotalPaise, Math.round(discountAmountINR * 100)))
	);

	// Expected total paise (subtotal - discount)
	const expectedTotalPaise = $derived(Math.max(0, subtotalPaise - discountAmountPaise));

	// Payment lines state
	interface PaymentLineState {
		method: 'cash' | 'upi' | 'card' | 'unpaid' | 'complimentary';
		amountINR: number;
		provider_reference_id: string;
	}

	let paymentLines = $state<PaymentLineState[]>([
		{ method: 'cash', amountINR: 0, provider_reference_id: '' }
	]);

	// Auto-adjust single payment line to match expected total
	$effect(() => {
		if (paymentLines.length === 1) {
			paymentLines[0].amountINR = expectedTotalPaise / 100;
		}
	});

	// Derived payment lines in paise for validation and API payload
	const paymentLinesPaise = $derived(
		paymentLines.map((line) => ({
			method: line.method,
			amount_paise: Math.round((line.amountINR || 0) * 100),
			provider_reference_id:
				line.method === 'upi' && line.provider_reference_id ? line.provider_reference_id : null
		}))
	);

	const sumPaymentsPaise = $derived(
		paymentLinesPaise.reduce((acc, line) => acc + line.amount_paise, 0)
	);

	const isMismatch = $derived(sumPaymentsPaise !== expectedTotalPaise);

	// ponytail: default to quick-cash, split payment only when toggled
	let showSplitPayment = $state<boolean>(false);

	let isSubmitting = $state<boolean>(false);
	let errorMessage = $state<string>('');
	let attemptedSubmit = $state<boolean>(false);

	function addPaymentLine() {
		const currentSumINR = paymentLines.reduce((acc, l) => acc + (l.amountINR || 0), 0);
		const remainingINR = Math.max(0, (expectedTotalPaise - Math.round(currentSumINR * 100)) / 100);
		paymentLines.push({
			method: 'cash',
			amountINR: remainingINR,
			provider_reference_id: ''
		});
	}

	function removePaymentLine(index: number) {
		paymentLines.splice(index, 1);
	}

	async function handleQuickCash() {
		errorMessage = '';
		isSubmitting = true;
		try {
			await store.completeService(entry.id, {
				queue_entry_id: entry.id,
				discount_amount_paise: discountAmountPaise,
				discount_reason: discountAmountPaise > 0 ? discountReason || 'Discount applied' : null,
				product_line_items: [],
				payment_lines: [{ method: 'cash', amount_paise: expectedTotalPaise, provider_reference_id: null }]
			});
			onClose();
		} catch (err: any) {
			errorMessage = err?.data?.message || 'An error occurred during checkout submission.';
		} finally {
			isSubmitting = false;
		}
	}

	async function handleSubmit(e: Event) {
		e.preventDefault();
		attemptedSubmit = true;

		if (isMismatch) {
			errorMessage = `Validation Error: Total payments (₹${(sumPaymentsPaise / 100).toFixed(2)}) must equal Expected Total (₹${(expectedTotalPaise / 100).toFixed(2)}).`;
			return;
		}

		errorMessage = '';
		isSubmitting = true;

		try {
			const checkoutRequest = {
				queue_entry_id: entry.id,
				discount_amount_paise: discountAmountPaise,
				discount_reason: discountAmountPaise > 0 ? discountReason || 'Discount applied' : null,
				product_line_items: [],
				payment_lines: paymentLinesPaise
			};

			await store.completeService(entry.id, checkoutRequest);
			onClose();
		} catch (err: any) {
			console.error('[Checkout] Completion failed:', err);
			errorMessage = err?.data?.message || 'An error occurred during checkout submission.';
		} finally {
			isSubmitting = false;
		}
	}
</script>

<div
	class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm"
	role="dialog"
	aria-modal="true"
>
	<div
		class="w-full max-w-lg bg-matte border border-white/[0.05] rounded-2xl shadow-2xl overflow-hidden text-primary flex flex-col max-h-[90vh]"
	>
		<!-- Modal Header -->
		<div class="px-6 py-4 border-b border-white/[0.05] flex justify-between items-center bg-canvas">
			<div>
				<h2 class="text-xl font-bold tracking-tight">Complete Service & Checkout</h2>
				<p class="text-xs text-muted">
					Token #{entry.token_number} — {entry.customer?.name || 'Walk-in Customer'}
				</p>
			</div>
			<button
				type="button"
				class="text-muted hover:text-primary transition-colors p-1"
				onclick={onClose}
				aria-label="Close modal"
			>
				<svg
					xmlns="http://www.w3.org/2000/svg"
					class="h-6 w-6"
					fill="none"
					viewBox="0 0 24 24"
					stroke="currentColor"
				>
					<path
						stroke-linecap="round"
						stroke-linejoin="round"
						stroke-width="2"
						d="M6 18L18 6M6 6l12 12"
					/>
				</svg>
			</button>
		</div>

		<!-- Modal Body -->
		<div class="flex-1 overflow-y-auto p-6 space-y-6">
			<!-- Service Line Items (Read-Only) -->
			<div>
				<h3 class="text-xs font-semibold text-muted uppercase tracking-wider mb-2">
					Rendered Services
				</h3>
				<div class="bg-canvas border border-white/[0.05] rounded-xl divide-y divide-slate-800">
					{#each entry.services as service}
						<div class="px-4 py-3 flex justify-between text-sm">
							<span class="font-medium text-primary">{service.name}</span>
							<div class="text-right">
								<div class="font-bold text-primary">
									₹{(service.price_paise / 100).toFixed(2)}
								</div>
								<div class="text-xs text-muted">{service.duration_minutes} mins</div>
							</div>
						</div>
					{/each}
				</div>
			</div>

			<!-- Summary -->
			<div class="bg-canvas border border-white/[0.05] rounded-xl p-4 space-y-2 text-sm">
				<div class="flex justify-between">
					<span class="text-muted">Services Subtotal:</span>
					<span class="font-medium text-primary">₹{(subtotalPaise / 100).toFixed(2)}</span>
				</div>
				{#if discountAmountPaise > 0}
					<div class="flex justify-between text-system-success">
						<span>Discount:</span>
						<span>-₹{(discountAmountPaise / 100).toFixed(2)}</span>
					</div>
				{/if}
				<div
					class="border-t border-white/[0.05] pt-2 flex justify-between font-semibold text-primary"
				>
					<span>Total:</span>
					<span>₹{(expectedTotalPaise / 100).toFixed(2)}</span>
				</div>
			</div>

			<!-- Quick Cash (default) or Split Payment -->
			{#if !showSplitPayment}
				<!-- Quick Cash Mode -->
				{#if errorMessage}
					<div class="bg-red-950/40 border border-red-900/50 rounded-xl p-3 text-xs text-red-400">
						{errorMessage}
					</div>
				{/if}

				<div class="space-y-3">
					<button
						type="button"
						class="w-full bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] disabled:opacity-40 text-canvas font-bold py-3.5 rounded-xl transition-all duration-150 text-base cursor-pointer"
						disabled={isSubmitting}
						onclick={handleQuickCash}
					>
						{isSubmitting ? 'Completing...' : `Complete — ₹${(expectedTotalPaise / 100).toFixed(2)} Cash`}
					</button>

					<div class="flex items-center justify-between">
						<button
							type="button"
							class="text-xs text-muted hover:text-primary transition-colors cursor-pointer"
							onclick={() => { showSplitPayment = true; }}
						>
							Split payment, UPI, or discount →
						</button>
						<button
							type="button"
							class="text-xs text-muted hover:text-primary transition-colors cursor-pointer"
							onclick={onClose}
						>
							Cancel
						</button>
					</div>
				</div>
			{:else}
				<!-- Split Payment Mode -->
				<form onsubmit={handleSubmit} class="space-y-6">
					<!-- Discount Section -->
					<div class="space-y-3">
						<h3 class="text-xs font-semibold text-muted uppercase tracking-wider">Discount</h3>
						<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
							<div>
								<label for="discount-amt" class="block text-xs font-medium text-muted mb-1">Discount Amount (₹)</label>
								<input type="number" id="discount-amt" step="1" min="0" max={subtotalPaise / 100}
									class="w-full bg-canvas border border-white/[0.05] rounded-xl px-3 py-2 text-sm text-primary focus:outline-none focus:border-gold-accent"
									bind:value={discountAmountINR} />
							</div>
							<div>
								<label for="discount-reason" class="block text-xs font-medium text-muted mb-1">Discount Reason</label>
								<input type="text" id="discount-reason" placeholder="Reason (required for discount)" required={discountAmountPaise > 0}
									class="w-full bg-canvas border border-white/[0.05] rounded-xl px-3 py-2 text-sm text-primary focus:outline-none focus:border-gold-accent placeholder:text-dim"
									bind:value={discountReason} />
							</div>
						</div>
					</div>

					<!-- Payment Lines -->
					<div class="space-y-3">
						<div class="flex justify-between items-center">
							<h3 class="text-xs font-semibold text-muted uppercase tracking-wider">Payment Lines</h3>
							<button type="button" class="text-xs text-gold-accent hover:text-gold-accent/80 font-medium transition-colors" onclick={addPaymentLine}>+ Add Line</button>
						</div>
						<div class="space-y-3">
							{#each paymentLines as line, idx}
								<div class="bg-canvas border border-white/[0.05] rounded-xl p-3 space-y-2 relative">
									{#if paymentLines.length > 1}
										<button type="button" class="absolute top-2 right-2 text-dim hover:text-red-400 transition-colors" onclick={() => removePaymentLine(idx)} aria-label="Remove payment line">
											<svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg>
										</button>
									{/if}
									<div class="grid grid-cols-2 gap-2 pr-6">
										<div>
											<label for="payment-method-{idx}" class="block text-[10px] font-medium text-dim mb-1">Method</label>
											<select id="payment-method-{idx}" class="w-full bg-matte border border-white/[0.05] rounded-lg px-2 py-1.5 text-xs text-primary focus:outline-none focus:border-gold-accent" bind:value={line.method}>
												<option value="cash">Cash</option>
												<option value="upi">UPI</option>
												<option value="card">Card</option>
												<option value="unpaid">Unpaid</option>
												<option value="complimentary">Complimentary</option>
											</select>
										</div>
										<div>
											<label for="payment-amount-{idx}" class="block text-[10px] font-medium text-dim mb-1">Amount (₹)</label>
											<input type="number" id="payment-amount-{idx}" step="1" min="0"
												class="w-full bg-matte border border-white/[0.05] rounded-lg px-2 py-1.5 text-xs text-primary focus:outline-none focus:border-gold-accent"
												bind:value={line.amountINR} />
										</div>
									</div>
									{#if line.method === 'upi'}
										<div class="pt-1">
											<label for="payment-upi-ref-{idx}" class="block text-[10px] font-medium text-dim mb-1">UPI Ref ID (Optional)</label>
											<input type="text" id="payment-upi-ref-{idx}" placeholder="Transaction reference"
												class="w-full bg-matte border border-white/[0.05] rounded-lg px-2 py-1.5 text-xs text-primary focus:outline-none focus:border-gold-accent placeholder:text-dim"
												bind:value={line.provider_reference_id} />
										</div>
									{/if}
								</div>
							{/each}
						</div>
					</div>

					<!-- Payment validation -->
					<div class="flex justify-between text-xs">
						<span class="text-muted">Entered Payments:</span>
						<span class={isMismatch ? 'text-system-warning font-bold' : 'text-system-success font-bold'}>
							₹{(sumPaymentsPaise / 100).toFixed(2)}
						</span>
					</div>

					{#if errorMessage || (mounted && isMismatch)}
						<div class="bg-red-950/40 border border-red-900/50 rounded-xl p-3 text-xs text-red-400">
							{errorMessage || 'Payment lines must sum exactly to the total.'}
						</div>
					{/if}

					<div class="flex space-x-3 pt-2">
						<button type="button" class="w-1/2 bg-surface hover:bg-titanium text-primary font-semibold py-2.5 rounded-xl transition-all duration-150 text-sm cursor-pointer"
							onclick={() => { showSplitPayment = false; errorMessage = ''; }}>
							← Quick Cash
						</button>
						<button type="submit"
							class="w-1/2 bg-gold-accent hover:brightness-110 active:brightness-90 disabled:opacity-40 text-canvas font-bold py-2.5 rounded-xl transition-all duration-150 text-sm cursor-pointer"
							disabled={isMismatch || isSubmitting}>
							{isSubmitting ? 'Completing...' : 'Complete Checkout'}
						</button>
					</div>
				</form>
			{/if}
		</div>
	</div>
</div>
