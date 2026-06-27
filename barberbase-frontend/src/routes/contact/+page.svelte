<script lang="ts">
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import {
		LEGAL_ENTITY_NAME,
		UDYAM_NUMBER,
		REGISTERED_ADDRESS,
		CONTACT_EMAIL,
		CONTACT_PHONE,
		BRAND
	} from '$lib/site-config';
</script>

<svelte:head>
	<title>Contact Us — {BRAND}</title>
</svelte:head>

<div class="min-h-screen bg-canvas text-primary font-manrope flex flex-col">
	<SiteHeader activePage="/contact" />

	<main id="main-content" class="flex-grow w-full max-w-3xl mx-auto px-6 py-12 md:py-20 space-y-10">
		<div class="space-y-3">
			<h1 class="font-satoshi font-extrabold text-3xl md:text-4xl tracking-[-0.03em]" style="text-wrap: balance;">
				Contact Us
			</h1>
			<p class="text-muted leading-relaxed">Have questions about {BRAND}? Get in touch.</p>
		</div>

		<div class="grid grid-cols-1 md:grid-cols-2 gap-px bg-white/[0.03] rounded-xl overflow-hidden">
			<a href="mailto:{CONTACT_EMAIL}" class="bg-canvas p-6 md:p-8 space-y-3 hover:bg-white/[0.01] transition-colors group">
				<div class="flex items-center gap-3">
					<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" class="text-gold-accent shrink-0">
						<rect x="2" y="4" width="20" height="16" rx="2"/><path d="m22 7-8.97 5.7a1.94 1.94 0 01-2.06 0L2 7"/>
					</svg>
					<span class="font-satoshi font-semibold text-sm group-hover:text-gold-accent transition-colors">Email</span>
				</div>
				<p class="font-mono text-sm text-gold-accent">{CONTACT_EMAIL}</p>
				<p class="text-xs text-muted">General inquiries, sales, and support.</p>
			</a>
			<a href="tel:{CONTACT_PHONE.replace(/\s+/g, '')}" class="bg-canvas p-6 md:p-8 space-y-3 hover:bg-white/[0.01] transition-colors group">
				<div class="flex items-center gap-3">
					<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" class="text-gold-accent shrink-0">
						<path d="M22 16.92v3a2 2 0 01-2.18 2 19.79 19.79 0 01-8.63-3.07 19.5 19.5 0 01-6-6A19.79 19.79 0 012.12 4.11 2 2 0 014.11 2h3a2 2 0 012 1.72c.127.96.361 1.903.7 2.81a2 2 0 01-.45 2.11L8.09 9.91a16 16 0 006 6l1.27-1.27a2 2 0 012.11-.45c.907.339 1.85.573 2.81.7A2 2 0 0122 16.92z"/>
					</svg>
					<span class="font-satoshi font-semibold text-sm group-hover:text-gold-accent transition-colors">Call or WhatsApp</span>
				</div>
				<p class="font-mono text-sm text-gold-accent">{CONTACT_PHONE}</p>
				<p class="text-xs text-muted">Mon–Sat, 10 AM – 7 PM IST.</p>
			</a>
		</div>

		<section class="bg-matte border border-white/[0.03] rounded-xl machined-edge p-6 md:p-8 space-y-5">
			<h2 class="font-satoshi font-bold text-lg">Business registration</h2>
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
			</div>
		</section>
	</main>

	<SiteFooter />
</div>
