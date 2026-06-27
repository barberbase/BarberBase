<script lang="ts">
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import {
		LEGAL_ENTITY_NAME,
		CONTACT_EMAIL,
		CONTACT_PHONE,
		BRAND
	} from '$lib/site-config';
</script>

<svelte:head>
	<title>Terms of Service — {BRAND}</title>
</svelte:head>

<div class="min-h-screen bg-canvas text-primary font-manrope flex flex-col">
	<SiteHeader activePage="/terms" />

	<main class="flex-grow w-full max-w-3xl mx-auto px-6 py-12 md:py-20 space-y-10">
		<div class="space-y-3">
			<h1 class="font-satoshi font-extrabold text-3xl md:text-4xl tracking-[-0.03em]" style="text-wrap: balance;">
				Terms of Service
			</h1>
			<p class="text-sm text-muted">These terms govern use of {BRAND} for customers and participating barbershops.</p>
			<p class="text-xs text-dim font-mono">Last updated: June 20, 2026</p>
		</div>

		<article class="space-y-8 text-sm leading-relaxed max-w-[65ch]">
			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">1. Acceptance of Terms</h2>
				<p class="text-muted">By checking into a virtual queue or logging into the staff/client portals of {BRAND}, operated by {LEGAL_ENTITY_NAME}, you agree to these Terms of Service. If you do not agree, you may not use the Service.</p>
			</section>

			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">2. Authentication & Phone Identity</h2>
				<p class="text-muted">Our Service uses phone number authentication:</p>
				<ul class="list-disc pl-5 space-y-2 text-muted marker:text-dim">
					<li>You represent that the phone number you provide belongs to you and is actively monitored.</li>
					<li>You consent to receive transactional WhatsApp messages related to your queue check-ins, delivered via <span class="font-mono text-gold-accent">Bhejna</span>.</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">3. Virtual Queue Guidelines</h2>
				<p class="text-muted">We provide virtual queuing for walk-ins at participating barbershops. Users agree:</p>
				<ul class="list-disc pl-5 space-y-2 text-muted marker:text-dim">
					<li>Queue positions are for immediate personal use. Booking slots using false identities or automated scripts is prohibited.</li>
					<li>Participating barbershops maintain authority to adjust queue order, delay slots, remove entries, or mark no-shows per their store policies.</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">4. Magic Link Security</h2>
				<p class="text-muted">Access to your queue status is granted via short-lived <strong class="text-primary">23-hour magic links</strong>. You are responsible for:</p>
				<ul class="list-disc pl-5 space-y-2 text-muted marker:text-dim">
					<li>Maintaining confidentiality of these links. Sharing them exposes your phone number and visit details.</li>
					<li>Requesting a new link when your session expires.</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">5. Service Fees & Notifications</h2>
				<p class="text-muted">{BRAND} does not charge customers to check into a queue. However:</p>
				<ul class="list-disc pl-5 space-y-2 text-muted marker:text-dim">
					<li>We do not guarantee instant or uninterrupted WhatsApp delivery. Delays from carrier networks or API limits are beyond our control.</li>
					<li>Standard data fees from your network provider may apply.</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">6. Limitation of Liability</h2>
				<p class="text-muted">{LEGAL_ENTITY_NAME} provides {BRAND} on an "as is" basis. We do not guarantee wait time accuracy, and we are not liable for disputes at the barbershop or missed walk-ins due to notification failures.</p>
			</section>

			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">7. Governing Law</h2>
				<p class="text-muted">These terms are governed by the laws of India. Disputes are subject to the exclusive jurisdiction of courts in Thane/Mumbai, Maharashtra.</p>
			</section>
		</article>
	</main>

	<SiteFooter />
</div>
