<script lang="ts">
	/* Appointment management is deferred to Phase 1.5. Pure stub page. */
	import Icon from '$lib/components/Icon.svelte';
</script>

<svelte:head>
	<title>Appointment Management — BarberBase</title>
	<meta name="robots" content="noindex" />
</svelte:head>

<div
	class="min-h-screen bg-canvas text-primary flex flex-col items-center justify-center p-4 md:p-6 font-manrope"
>
	<div
		class="w-full max-w-md bg-matte border border-white/[0.03] rounded-3xl p-8 shadow-2xl space-y-6 text-center relative overflow-hidden"
	>
		<!-- Subtle ambient backdrop light glow -->
		<div
			class="absolute -top-24 -left-24 w-48 h-48 bg-gold-accent/10 rounded-full blur-3xl pointer-events-none"
		></div>
		<div
			class="absolute -bottom-24 -right-24 w-48 h-48 bg-gold-accent/5 rounded-full blur-3xl pointer-events-none"
		></div>

		<div class="mx-auto w-16 h-16 rounded-full bg-canvas/60 border border-white/[0.06] flex items-center justify-center text-gold-accent">
			<Icon name="calendar" size={28} />
		</div>

		<div class="space-y-3">
			<span class="inline-block font-mono text-[10px] font-medium uppercase tracking-[0.25em] text-muted border border-white/[0.08] bg-titanium rounded px-2.5 py-1"
				>Coming Soon</span
			>
			<h1 class="text-2xl font-extrabold text-primary tracking-tight">Appointments</h1>
		</div>

		<div class="space-y-2 py-2">
			<p class="text-sm text-primary font-semibold">
				Online appointment booking is on its way.
			</p>
			<p class="text-xs text-muted leading-relaxed">
				For now, walk-ins and WhatsApp queue joins work as usual — your live queue status
				stays in the WhatsApp link we sent you.
			</p>
		</div>

		<div class="pt-4 border-t border-white/[0.04]">
			<span class="font-mono text-[10px] font-medium text-dim uppercase tracking-[0.25em]"
				>BarberBase</span
			>
		</div>
	</div>
</div>
