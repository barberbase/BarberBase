<script lang="ts">
	import { enhance } from '$app/forms';

	let { form } = $props<{
		form: {
			step?: 'phone' | 'otp';
			phone_number?: string;
			error?: string;
		};
	}>();

	// Local state management using Svelte 5 Runes
	let step = $state<'phone' | 'otp'>('phone');
	let phoneNumber = $state<string>('');
	let otp = $state<string>('');
	let loading = $state<boolean>(false);

	// Sync local state when form props change from server-side action responses
	$effect(() => {
		if (form?.step) {
			step = form.step;
		}
		if (form?.phone_number) {
			phoneNumber = form.phone_number;
		}
	});

	// Handle going back to phone input step
	function handleChangeNumber() {
		step = 'phone';
		otp = '';
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
			class="absolute bottom-1/4 left-1/3 w-[400px] h-[400px] rounded-full bg-blue-500/5 blur-[100px]"
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
							class="w-full bg-canvas border border-white/[0.03] rounded-2xl px-4 py-4 text-primary placeholder:text-dim focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-amber-500 transition-all duration-200 text-base"
						/>
					</div>
					<span class="text-[11px] text-dim block">
						10-digit number will automatically prefix with +91. OTP will be sent via WhatsApp.
					</span>
				</div>

				<!-- Inline Error Display -->
				{#if form?.error && step === 'phone'}
					<div
						class="bg-red-950/30 border border-system-error/30 rounded-2xl p-4 text-sm text-system-error/80 flex items-start space-x-3 animate-fade-in"
					>
						<svg
							xmlns="http://www.w3.org/2000/svg"
							class="h-5 w-5 shrink-0 mt-0.5"
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
						<div>{form.error}</div>
					</div>
				{/if}

				<button
					type="submit"
					disabled={loading || !phoneNumber}
					class="w-full py-4 bg-gold-accent hover:bg-amber-400 active:bg-amber-600 disabled:opacity-40 disabled:hover:bg-gold-accent text-canvas font-bold text-base rounded-2xl transition-all duration-150 shadow-lg cursor-pointer flex items-center justify-center space-x-2"
				>
					{#if loading}
						<svg
							class="animate-spin h-5 w-5 text-slate-950"
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
						<span class="text-xs text-gold-accent font-medium">Sent to {phoneNumber}</span>
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
						class="w-full bg-canvas border border-white/[0.03] rounded-2xl px-4 py-4 text-primary placeholder:text-dim focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-amber-500 tracking-[0.5em] text-center font-bold text-xl transition-all duration-200"
					/>
					<span class="text-[11px] text-dim block text-center">
						OTP is valid for 5 minutes.
					</span>
				</div>

				<!-- Inline Error Display -->
				{#if form?.error && step === 'otp'}
					<div
						class="bg-red-950/30 border border-system-error/30 rounded-2xl p-4 text-sm text-system-error/80 flex items-start space-x-3 animate-fade-in"
					>
						<svg
							xmlns="http://www.w3.org/2000/svg"
							class="h-5 w-5 shrink-0 mt-0.5"
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
						<div>{form.error}</div>
					</div>
				{/if}

				<div class="space-y-3">
					<button
						type="submit"
						disabled={loading || otp.length !== 6}
						class="w-full py-4 bg-gold-accent hover:bg-amber-400 active:bg-amber-600 disabled:opacity-40 disabled:hover:bg-gold-accent text-canvas font-bold text-base rounded-2xl transition-all duration-150 shadow-lg cursor-pointer flex items-center justify-center space-x-2"
					>
						{#if loading}
							<svg
								class="animate-spin h-5 w-5 text-slate-950"
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
						class="w-full py-3 bg-transparent hover:bg-slate-800/40 text-muted hover:text-primary font-semibold text-sm rounded-2xl transition-all duration-150 cursor-pointer text-center"
					>
						Change number
					</button>
				</div>
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
