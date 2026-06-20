<script lang="ts">
	import {
		LEGAL_ENTITY_NAME,
		UDYAM_NUMBER,
		REGISTERED_ADDRESS,
		CONTACT_EMAIL,
		CONTACT_PHONE
	} from '$lib/site-config';
	const currentYear = new Date().getFullYear();
</script>

<footer class="w-full bg-slate-950 border-t border-slate-900 py-12 px-4 mt-auto">
	<div
		class="max-w-4xl mx-auto flex flex-col items-center text-center space-y-6 text-slate-400 text-sm"
	>
		<!-- Brand & Entity Bridge -->
		<div class="space-y-1">
			<p class="text-slate-200 font-semibold">BarberBase — a product of {LEGAL_ENTITY_NAME}</p>
			<p class="text-xs text-slate-500 font-mono">
				{LEGAL_ENTITY_NAME} &middot; Udyam Reg. No. {UDYAM_NUMBER}
			</p>
		</div>

		<!-- Registered Address -->
		<div class="max-w-xl text-xs text-slate-500 leading-relaxed">
			<span class="font-semibold text-slate-400">Registered Address:</span>
			{REGISTERED_ADDRESS}
		</div>

		<!-- Contact Information -->
		<div class="flex flex-wrap justify-center gap-x-4 gap-y-1 text-xs text-slate-400">
			<a href="mailto:{CONTACT_EMAIL}" class="hover:text-amber-500 transition-colors"
				>{CONTACT_EMAIL}</a
			>
			<span class="text-slate-700">&middot;</span>
			<a
				href="tel:{CONTACT_PHONE.replace(/\s+/g, '')}"
				class="hover:text-amber-500 transition-colors">{CONTACT_PHONE}</a
			>
		</div>

		<!-- Navigation Links -->
		<nav class="flex flex-wrap justify-center gap-x-6 gap-y-2 text-xs font-medium text-slate-300">
			<a href="/about" class="hover:text-amber-500 transition-colors">About Us</a>
			<a href="/contact" class="hover:text-amber-500 transition-colors">Contact</a>
			<a href="/privacy" class="hover:text-amber-500 transition-colors">Privacy Policy</a>
			<a href="/terms" class="hover:text-amber-500 transition-colors">Terms of Service</a>
		</nav>

		<!-- Copyright -->
		<div class="pt-4 border-t border-slate-900 w-full text-xs text-slate-600">
			&copy; {currentYear}
			{LEGAL_ENTITY_NAME}. All rights reserved.
		</div>
	</div>
</footer>
