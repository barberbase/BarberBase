<script lang="ts">
	import { enhance } from '$app/forms';

	let { form } = $props<{
		form: {
			error?: string;
		};
	}>();

	let password = $state<string>('');
	let loading = $state<boolean>(false);
</script>

<svelte:head>
	<title>Operator Login — BarberBase</title>
</svelte:head>

<div
	class="min-h-screen bg-slate-950 text-slate-100 flex flex-col justify-center items-center p-4 font-sans selection:bg-amber-500 selection:text-slate-950"
>
	<!-- Background Decorative Gradients -->
	<div class="absolute inset-0 overflow-hidden pointer-events-none">
		<div
			class="absolute top-1/4 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[500px] h-[500px] rounded-full bg-amber-500/10 blur-[120px]"
		></div>
		<div
			class="absolute bottom-1/4 left-1/3 w-[400px] h-[400px] rounded-full bg-blue-500/5 blur-[100px]"
		></div>
	</div>

	<!-- Login Card -->
	<div
		class="relative w-full max-w-md bg-slate-900/60 backdrop-blur-xl border border-slate-800/80 rounded-3xl p-8 shadow-2xl space-y-8"
	>
		<!-- Header -->
		<div class="text-center space-y-2">
			<h1 class="text-3xl font-extrabold text-amber-500 tracking-wider">BarberBase</h1>
			<p class="text-sm font-semibold text-slate-400">Operator Console Access</p>
		</div>

		<form
			method="POST"
			action="?/login"
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
					for="password"
					class="block text-xs font-semibold text-slate-400 uppercase tracking-wider"
				>
					Console Password
				</label>
				<input
					type="password"
					id="password"
					name="password"
					placeholder="••••••••"
					required
					disabled={loading}
					bind:value={password}
					class="w-full bg-slate-950 border border-slate-800 rounded-2xl px-4 py-4 text-slate-100 placeholder:text-slate-700 focus:outline-none focus:border-amber-500 focus:ring-1 focus:ring-amber-500 transition-all duration-200 text-base"
				/>
			</div>

			<!-- Inline Error Display -->
			{#if form?.error}
				<div
					class="bg-red-950/30 border border-red-900/50 rounded-2xl p-4 text-sm text-red-400 flex items-start space-x-3 animate-fade-in"
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
				disabled={loading || !password}
				class="w-full py-4 bg-amber-500 hover:bg-amber-400 active:bg-amber-600 disabled:opacity-40 disabled:hover:bg-amber-500 text-slate-950 font-bold text-base rounded-2xl transition-all duration-150 shadow-lg cursor-pointer flex items-center justify-center space-x-2"
			>
				{#if loading}
					<svg
						class="animate-spin h-5 w-5 text-slate-950"
						xmlns="http://www.w3.org/2000/svg"
						fill="none"
						viewBox="0 0 24 24"
					>
						<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"
						></circle>
						<path
							class="opacity-75"
							fill="currentColor"
							d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
						></path>
					</svg>
					<span>Unlocking...</span>
				{:else}
					<span>Unlock Console</span>
				{/if}
			</button>
		</form>
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
