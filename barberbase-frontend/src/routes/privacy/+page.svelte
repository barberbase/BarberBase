<script lang="ts">
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import {
		LEGAL_ENTITY_NAME,
		CONTACT_EMAIL,
		BRAND
	} from '$lib/site-config';
</script>

<svelte:head>
	<title>Privacy Policy — {BRAND}</title>
</svelte:head>

<div class="min-h-screen bg-canvas text-primary font-manrope flex flex-col">
	<SiteHeader />

	<main class="flex-grow w-full max-w-3xl mx-auto px-6 py-12 md:py-20 space-y-10">
		<div class="space-y-3">
			<h1 class="font-satoshi font-extrabold text-3xl md:text-4xl tracking-[-0.03em]" style="text-wrap: balance;">
				Privacy Policy
			</h1>
			<p class="text-xs text-dim font-mono">Last updated: June 20, 2026</p>
		</div>

		<article class="space-y-8 text-sm leading-relaxed max-w-[65ch]">
			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">1. Information We Collect</h2>
				<p class="text-muted">To provide virtual queuing and real-time updates, {BRAND} collects the minimum necessary data:</p>
				<ul class="list-disc pl-5 space-y-2 text-muted marker:text-dim">
					<li><strong class="text-primary">Phone Number</strong> — your unique identity, verified via WhatsApp OTP.</li>
					<li><strong class="text-primary">Queue & Visit History</strong> — which shop you joined, services selected, timestamps, and queue status.</li>
					<li><strong class="text-primary">Technical Data</strong> — IP address, browser type, and SSE connection logs for maintaining real-time streams.</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">2. How We Use Your Data</h2>
				<p class="text-muted">We use collected information solely for operating the queue service:</p>
				<ul class="list-disc pl-5 space-y-2 text-muted marker:text-dim">
					<li><strong class="text-primary">Queue Operations</strong> — allowing shops to view waiting customers, update statuses, and notify turns.</li>
					<li><strong class="text-primary">Notification Delivery</strong> — sending queue updates via WhatsApp through <span class="font-mono text-gold-accent">Bhejna</span>.</li>
					<li><strong class="text-primary">Secure Access</strong> — generating short-lived 23-hour magic-link tokens that auto-expire.</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">3. Data Sharing</h2>
				<p class="text-muted">We do not sell, rent, or trade your data. Sharing happens only when:</p>
				<ul class="list-disc pl-5 space-y-2 text-muted marker:text-dim">
					<li><strong class="text-primary">Partner Barbershops</strong> — staff can view your name, phone number, and services when you join their queue.</li>
					<li><strong class="text-primary">WhatsApp API</strong> — phone numbers are routed through the WhatsApp Business API for notifications.</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">4. Data Retention</h2>
				<p class="text-muted">Queue entries are maintained active during the visit day. After completion, cancellation, or no-show, personal details are archived and only anonymized metrics are kept for analytics.</p>
			</section>

			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">5. Security</h2>
				<p class="text-muted">We use industry-standard measures: route protection, secure API tokens, and 23-hour magic-link expiration to limit exposure of personal dashboards.</p>
			</section>

			<section class="space-y-3">
				<h2 class="font-satoshi font-bold text-base">6. Your Rights</h2>
				<p class="text-muted">You can request access to or deletion of your data by contacting <a href="mailto:{CONTACT_EMAIL}" class="text-gold-accent hover:text-gold-accent/80 transition-colors">{CONTACT_EMAIL}</a>.</p>
			</section>
		</article>
	</main>

	<SiteFooter />
</div>
