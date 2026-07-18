<script lang="ts">
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import Seo from '$lib/components/Seo.svelte';
	import {
		LEGAL_ENTITY_NAME,
		UDYAM_NUMBER,
		REGISTERED_ADDRESS,
		CONTACT_EMAIL,
		CONTACT_PHONE,
		BRAND,
		ORGANIZATION_JSON_LD
	} from '$lib/site-config';
</script>

<Seo
	title="About Us — {BRAND}"
	description="{BRAND} is built by {LEGAL_ENTITY_NAME} — real-time walk-in queue and appointment management for barbershops."
	path="/about"
/>

<svelte:head>
	{@html `<script type="application/ld+json">${JSON.stringify(ORGANIZATION_JSON_LD).replace(/</g, '\\u003c')}</script>`}
</svelte:head>

<div class="min-h-screen bg-canvas text-primary font-manrope flex flex-col">
	<SiteHeader activePage="/about" />

	<main id="main-content" class="flex-grow w-full max-w-4xl mx-auto px-6 py-12 md:py-20 space-y-12">
		<div class="space-y-4">
			<h1 class="font-satoshi font-extrabold text-3xl md:text-4xl tracking-[-0.03em]" style="text-wrap: balance;">
				About Us
			</h1>
			<p class="text-lg text-muted leading-relaxed max-w-[65ch]">
				{BRAND} lets barbershop customers join a queue or book an appointment from their phone — no app, no account — and get WhatsApp updates as their turn approaches. Staff run walk-ins, appointments, and checkouts from one dashboard.
			</p>
			<p class="text-sm text-muted max-w-[65ch]">
				{BRAND} is built and operated by <a href="https://codenxtlab.tech/" target="_blank" rel="noopener noreferrer" class="text-gold-accent hover:text-gold-accent/80 transition-colors">{LEGAL_ENTITY_NAME}</a>, a registered MSME (Udyam {UDYAM_NUMBER}).
			</p>
		</div>

		<section class="space-y-4">
			<h2 class="font-satoshi font-bold text-xl tracking-[-0.02em]">Why this exists</h2>
			<div class="space-y-3 text-muted leading-relaxed max-w-[65ch]">
				<p>Traditional booking calendars do not map cleanly to walk-in shops. One slow haircut can offset the entire schedule, and customers waiting physically create avoidable crowding.</p>
				<p>{BRAND} bridges physical walk-ins with a virtual queue: join from phone, track position live, and get WhatsApp alerts when the turn is near.</p>
			</div>
		</section>

		<section class="space-y-4">
			<h2 class="font-satoshi font-bold text-xl tracking-[-0.02em]">How we operate</h2>
			<div class="grid grid-cols-1 md:grid-cols-3 gap-px bg-white/[0.03] rounded-xl overflow-hidden">
				<div class="bg-canvas p-6 space-y-2">
					<h3 class="font-satoshi font-semibold text-sm">Phone-first access</h3>
					<p class="text-sm text-muted leading-relaxed">No app install, no passwords. Session links are short-lived and practical.</p>
				</div>
				<div class="bg-canvas p-6 space-y-2">
					<h3 class="font-satoshi font-semibold text-sm">Reliable alerts</h3>
					<p class="text-sm text-muted leading-relaxed">Queue updates dispatched through WhatsApp so customers know when to return.</p>
				</div>
				<div class="bg-canvas p-6 space-y-2">
					<h3 class="font-satoshi font-semibold text-sm">Privacy by design</h3>
					<p class="text-sm text-muted leading-relaxed">Access links expire and data retention is constrained to reduce unnecessary exposure.</p>
				</div>
			</div>
		</section>

		<section class="bg-matte border border-white/[0.03] rounded-xl machined-edge p-6 md:p-8 space-y-5">
			<h2 class="font-satoshi font-bold text-lg">Business verification</h2>
			<div class="grid grid-cols-1 md:grid-cols-2 gap-5 text-sm">
				<div class="space-y-1">
					<span class="block text-xs text-dim font-mono uppercase tracking-widestUI">Legal Entity</span>
					<span class="font-medium">{LEGAL_ENTITY_NAME}</span>
				</div>
				<div class="space-y-1">
					<span class="block text-xs text-dim font-mono uppercase tracking-widestUI">Udyam No.</span>
					<span class="font-mono">{UDYAM_NUMBER}</span>
				</div>
				<div class="space-y-1 md:col-span-2">
					<span class="block text-xs text-dim font-mono uppercase tracking-widestUI">Address</span>
					<span>{REGISTERED_ADDRESS}</span>
				</div>
				<div class="space-y-1">
					<span class="block text-xs text-dim font-mono uppercase tracking-widestUI">Email</span>
					<a href="mailto:{CONTACT_EMAIL}" class="text-gold-accent hover:text-gold-accent/80 transition-colors">{CONTACT_EMAIL}</a>
				</div>
				<div class="space-y-1">
					<span class="block text-xs text-dim font-mono uppercase tracking-widestUI">Phone</span>
					<a href="tel:{CONTACT_PHONE.replace(/\s+/g, '')}" class="text-gold-accent hover:text-gold-accent/80 transition-colors">{CONTACT_PHONE}</a>
				</div>
			</div>
		</section>
	</main>

	<SiteFooter />
</div>
