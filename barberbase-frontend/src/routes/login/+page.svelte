<script lang="ts">
	import { enhance } from '$app/forms';
	import Icon from '$lib/components/Icon.svelte';

	let { form } = $props<{
		form: {
			step?: 'phone' | 'otp';
			phone_number?: string;
			error?: string;
			code?: string;
		};
	}>();

	// Local state management using Svelte 5 Runes
	let step = $state<'phone' | 'otp'>('phone');
	let phoneNumber = $state<string>('');
	let otp = $state<string>('');
	let loading = $state<boolean>(false);
	let resending = $state<boolean>(false);
	let wrongAttempts = $state<number>(0);
	let locked = $state<boolean>(false);

	// Resend cooldown: 200s = the token bucket's refill interval (burst 3,
	// 1 token/200s), so waiting this long between sends never trips the limit.
	const RESEND_COOLDOWN_S = 200;
	let resendSecondsLeft = $state<number>(0);
	let resendTimer: ReturnType<typeof setInterval> | undefined;

	function startResendCooldown() {
		clearInterval(resendTimer);
		resendSecondsLeft = RESEND_COOLDOWN_S;
		resendTimer = setInterval(() => {
			resendSecondsLeft -= 1;
			if (resendSecondsLeft <= 0) {
				clearInterval(resendTimer);
			}
		}, 1000);
	}

	$effect(() => {
		return () => clearInterval(resendTimer);
	});

	function formatCountdown(s: number) {
		const m = Math.floor(s / 60);
		const rest = String(s % 60).padStart(2, '0');
		return `${m}:${rest}`;
	}

	// Sync local state when form props change from server-side action responses.
	// lastForm (non-reactive) makes each response process exactly once: without
	// it, wrongAttempts += 1 self-triggers the effect, and a `step` change
	// re-runs it against the same stale 401 response.
	let lastForm: unknown = null;
	$effect(() => {
		if (form === lastForm) return;
		lastForm = form;
		// A 429 on resend must not knock the user back to the phone step.
		if (form?.step && !(form.code === 'rate_limited' && step === 'otp')) {
			step = form.step;
		}
		if (form?.phone_number) {
			phoneNumber = form.phone_number;
		}
		if (form?.step === 'otp' && !form?.error) {
			// Successful send/resend: fresh OTP, reset wrong-attempt tracking.
			otp = '';
			wrongAttempts = 0;
			locked = false;
			startResendCooldown();
		}
		if (form?.code === 'invalid_otp') {
			wrongAttempts += 1;
			if (wrongAttempts >= 5) {
				locked = true;
			}
		}
		if (form?.code === 'rate_limited') {
			// Another tab/request burned the burst — restart the wait.
			startResendCooldown();
		}
	});

	// Handle going back to phone input step
	function handleChangeNumber() {
		step = 'phone';
		otp = '';
		clearInterval(resendTimer);
		resendSecondsLeft = 0;
	}
</script>

<svelte:head>
	<title>Staff Login — BarberBase</title>
</svelte:head>

<div
	class="min-h-screen bg-canvas text-primary flex flex-col justify-center items-center p-4 font-manrope"
>
	<!-- Background Decorative Gradients -->
	<div class="absolute inset-0 overflow-hidden pointer-events-none">
		<div
			class="absolute top-1/4 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[500px] h-[500px] rounded-full bg-gold-accent/10 blur-[120px]"
		></div>
		<div
			class="absolute bottom-1/4 left-1/3 w-[400px] h-[400px] rounded-full bg-gold-accent/5 blur-[100px]"
		></div>
	</div>

	<!-- Login Card -->
	<div
		class="relative w-full max-w-md bg-matte border border-white/[0.03] rounded-3xl p-8 shadow-2xl space-y-8"
	>
		<!-- Header -->
		<div class="text-center space-y-2">
			<h1 class="text-3xl font-extrabold text-gold-accent tracking-wider">BarberBase</h1>
			<p class="text-sm font-semibold text-muted">Staff Access Portal</p>
		</div>

		<!-- Step 1: Phone Number Input -->
		{#if step === 'phone'}
			<form
				method="POST"
				action="?/requestOtp"
				use:enhance={() => {
					loading = true;
					return async ({ update }) => {
						await update();
						loading = false;
					};
				}}
				class="space-y-6"
			>
				<div class="space-y-2">
					<label
						for="phone_number"
						class="block text-xs font-semibold text-muted uppercase tracking-wider"
					>
						WhatsApp Phone Number
					</label>
					<div class="relative">
						<input
							type="tel"
							id="phone_number"
							name="phone_number"
							placeholder="e.g. 9876543210 or +919876543210"
							required
							disabled={loading}
							bind:value={phoneNumber}
							class="w-full bg-canvas border border-white/[0.03] rounded-2xl px-4 py-4 text-primary placeholder:text-dim focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-gold-accent/30 transition-all duration-200 text-base"
						/>
					</div>
					<span class="text-[11px] text-dim block">
						10-digit number will automatically prefix with +91. OTP will be sent via WhatsApp.
					</span>
				</div>

				<!-- Inline Error Display -->
				{#if form?.error && step === 'phone'}
					<div
						class="bg-system-error/10 border border-system-error/30 rounded-2xl p-4 text-sm text-system-error flex items-start space-x-3 animate-fade-in"
					>
						<span class="shrink-0 mt-0.5"><Icon name="alert" size={18} /></span>
						<div>{form.error}</div>
					</div>
				{/if}

				<button
					type="submit"
					disabled={loading || !phoneNumber}
					class="w-full py-4 bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] disabled:opacity-40 disabled:hover:brightness-100 text-canvas font-bold text-base rounded-2xl transition-all duration-150 shadow-lg cursor-pointer flex items-center justify-center space-x-2"
				>
					{#if loading}
						<svg
							class="animate-spin h-5 w-5 text-canvas"
							xmlns="http://www.w3.org/2000/svg"
							fill="none"
							viewBox="0 0 24 24"
						>
							<circle
								class="opacity-25"
								cx="12"
								cy="12"
								r="10"
								stroke="currentColor"
								stroke-width="4"
							></circle>
							<path
								class="opacity-75"
								fill="currentColor"
								d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
							></path>
						</svg>
						<span>Sending OTP...</span>
					{:else}
						<span>Send OTP</span>
					{/if}
				</button>
			</form>
		{/if}

		<!-- Step 2: OTP Verification -->
		{#if step === 'otp'}
			<form
				method="POST"
				action="?/verifyOtp"
				use:enhance={() => {
					loading = true;
					return async ({ update }) => {
						await update();
						loading = false;
					};
				}}
				class="space-y-6"
			>
				<!-- Hidden Field to keep phone number in submission -->
				<input type="hidden" name="phone_number" value={phoneNumber} />

				<div class="space-y-2">
					<div class="flex justify-between items-center">
						<label
							for="otp"
							class="block text-xs font-semibold text-muted uppercase tracking-wider"
						>
							Enter 6-Digit OTP
						</label>
						<span class="text-xs text-gold-accent font-mono font-medium">Sent to {phoneNumber}</span>
					</div>
					<input
						type="text"
						id="otp"
						name="otp"
						placeholder="e.g. 123456"
						maxlength="6"
						required
						disabled={loading}
						bind:value={otp}
						oninput={() => { otp = otp.replace(/\D/g, '').slice(0, 6); }}
						class="w-full bg-canvas border border-white/[0.03] rounded-2xl px-4 py-4 text-primary placeholder:text-dim focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-gold-accent/30 tracking-[0.5em] text-center font-mono font-bold text-xl transition-all duration-200"
					/>
					<span class="text-[11px] text-dim block text-center">
						OTP is valid for 5 minutes.
					</span>
				</div>

				<!-- Inline Error Display -->
				{#if locked}
					<div
						class="bg-system-error/10 border border-system-error/30 rounded-2xl p-4 text-sm text-system-error flex items-start space-x-3 animate-fade-in"
					>
						<span class="shrink-0 mt-0.5"><Icon name="alert" size={18} /></span>
						<div>Too many wrong attempts — that code is now locked. Resend to get a new code.</div>
					</div>
				{:else if form?.error && step === 'otp'}
					<div
						class="bg-system-error/10 border border-system-error/30 rounded-2xl p-4 text-sm text-system-error flex items-start space-x-3 animate-fade-in"
					>
						<span class="shrink-0 mt-0.5"><Icon name="alert" size={18} /></span>
						<div>{form.error}</div>
					</div>
				{/if}

				<div class="space-y-3">
					<button
						type="submit"
						disabled={loading || locked || otp.length !== 6}
						class="w-full py-4 bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] disabled:opacity-40 disabled:hover:brightness-100 text-canvas font-bold text-base rounded-2xl transition-all duration-150 shadow-lg cursor-pointer flex items-center justify-center space-x-2"
					>
						{#if loading}
							<svg
								class="animate-spin h-5 w-5 text-canvas"
								xmlns="http://www.w3.org/2000/svg"
								fill="none"
								viewBox="0 0 24 24"
							>
								<circle
									class="opacity-25"
									cx="12"
									cy="12"
									r="10"
									stroke="currentColor"
									stroke-width="4"
								></circle>
								<path
									class="opacity-75"
									fill="currentColor"
									d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
								></path>
							</svg>
							<span>Verifying...</span>
						{:else}
							<span>Verify & Login</span>
						{/if}
					</button>

					<button
						type="button"
						disabled={loading}
						onclick={handleChangeNumber}
						class="w-full py-3 bg-transparent hover:bg-titanium text-muted hover:text-primary font-semibold text-sm rounded-2xl transition-all duration-150 cursor-pointer text-center"
					>
						Change number
					</button>
				</div>
			</form>

			<form
				method="POST"
				action="?/requestOtp"
				use:enhance={() => {
					resending = true;
					return async ({ update }) => {
						await update();
						resending = false;
					};
				}}
			>
				<input type="hidden" name="phone_number" value={phoneNumber} />
				<button
					type="submit"
					disabled={resending || resendSecondsLeft > 0}
					class="w-full py-3 bg-transparent hover:bg-titanium text-muted hover:text-primary font-semibold text-sm rounded-2xl transition-all duration-150 cursor-pointer text-center disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-muted"
				>
					{#if resendSecondsLeft > 0}
						Resend code in {formatCountdown(resendSecondsLeft)}
					{:else if resending}
						Resending...
					{:else}
						Resend code
					{/if}
				</button>
			</form>
		{/if}
	</div>
</div>

<style>
	@keyframes fadeIn {
		from {
			opacity: 0;
			transform: translateY(4px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}
	.animate-fade-in {
		animation: fadeIn 0.2s ease-out forwards;
	}
</style>
