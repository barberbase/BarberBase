<script lang="ts">
	import {
		LEGAL_ENTITY_NAME,
		REGISTERED_ADDRESS,
		CONTACT_EMAIL,
		CONTACT_PHONE
	} from '$lib/site-config';
	const currentYear = new Date().getFullYear();
</script>

<footer class="w-full bg-canvas border-t border-white/[0.03] py-12 px-4 mt-auto">
	<div
		class="max-w-4xl mx-auto flex flex-col items-center text-center space-y-6 text-muted text-sm"
	>
		<!-- Brand & Entity Bridge -->
		<div class="space-y-1">
			<p class="text-primary font-semibold">
				BarberBase — Powered by
				<a
					href="https://codenxtlab.tech/"
					target="_blank"
					rel="noopener noreferrer"
					class="text-gold-accent hover:text-gold-accent/80 transition-colors"
				>
					{LEGAL_ENTITY_NAME}
				</a>
			</p>
		</div>

		<!-- Registered Address -->
		<div class="max-w-xl text-xs text-dim leading-relaxed">
			<span class="font-semibold text-muted">Registered Address:</span>
			{REGISTERED_ADDRESS}
		</div>

		<!-- Contact Information -->
		<div class="flex flex-wrap justify-center gap-x-4 gap-y-1 text-xs text-muted">
			<a href="mailto:{CONTACT_EMAIL}" class="hover:text-gold-accent transition-colors"
				>{CONTACT_EMAIL}</a
			>
			<span class="text-dim">&middot;</span>
			<a
				href="tel:{CONTACT_PHONE.replace(/\s+/g, '')}"
				class="hover:text-gold-accent transition-colors">{CONTACT_PHONE}</a
			>
		</div>

		<!-- Navigation Links -->
		<nav aria-label="Footer" class="flex flex-wrap justify-center gap-x-6 gap-y-2 text-xs font-medium text-primary">
			<a href="/about" class="hover:text-gold-accent transition-colors">About Us</a>
			<a href="/contact" class="hover:text-gold-accent transition-colors">Contact</a>
			<a href="/privacy" class="hover:text-gold-accent transition-colors">Privacy Policy</a>
			<a href="/terms" class="hover:text-gold-accent transition-colors">Terms of Service</a>
		</nav>

		<!-- Copyright -->
		<div class="pt-4 border-t border-white/[0.03] w-full text-xs text-dim">
			&copy; {currentYear}
			{LEGAL_ENTITY_NAME}. All rights reserved.
		</div>
	</div>
</footer>
