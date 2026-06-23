<script lang="ts">
	/* Appointment management is deferred to Phase 1.5. Pure stub page. */
</script>

<svelte:head>
	<title>Appointment Management — BarberBase</title>
</svelte:head>

<div
	class="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-zinc-950 text-primary flex flex-col items-center justify-center p-4 md:p-6 font-manrope"
>
	<div
		class="w-full max-w-md bg-matte/60 backdrop-blur-xl border border-white/[0.03] rounded-3xl p-8 shadow-2xl space-y-6 text-center relative overflow-hidden"
	>
		<!-- Subtle ambient backdrop light glow -->
		<div
			class="absolute -top-24 -left-24 w-48 h-48 bg-gold-accent/10 rounded-full blur-3xl pointer-events-none"
		></div>
		<div
			class="absolute -bottom-24 -right-24 w-48 h-48 bg-gold-accent/5 rounded-full blur-3xl pointer-events-none"
		></div>

		<div class="text-5xl animate-pulse">📅</div>
		<h1 class="text-2xl font-black text-primary tracking-wide">Appointment Management</h1>

		<div class="space-y-2 py-2">
			<p class="text-sm text-primary font-semibold">Your appointment details will appear here.</p>
			<p class="text-xs text-muted">Appointment management is coming soon.</p>
		</div>

		<div class="pt-4 border-t border-white/[0.02]">
			<span class="text-[10px] font-bold text-gold-accent uppercase tracking-widest"
				>BarberBase Platform</span
			>
		</div>
	</div>
</div>
