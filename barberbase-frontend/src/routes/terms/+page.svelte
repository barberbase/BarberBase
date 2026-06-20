<script lang="ts">
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import { resolve } from '$app/paths';
	import logo from '$lib/assets/favicon.svg';
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
	<title>Terms of Service — {BRAND}</title>
</svelte:head>

<div
	class="min-h-screen bg-slate-950 text-slate-100 font-sans selection:bg-amber-500 selection:text-slate-950 flex flex-col relative overflow-hidden"
>
	<!-- Background Decorative Gradients -->
	<div class="absolute inset-0 overflow-hidden pointer-events-none z-0">
		<div
			class="absolute top-[-10%] left-[10%] w-[600px] h-[600px] rounded-full bg-amber-500/5 blur-[130px]"
		></div>
		<div
			class="absolute bottom-[10%] right-[10%] w-[500px] h-[500px] rounded-full bg-blue-500/5 blur-[120px]"
		></div>
	</div>

	<!-- Navigation Bar -->
	<header
		class="w-full max-w-6xl mx-auto px-6 py-6 flex justify-between items-center relative z-10 border-b border-slate-900"
	>
		<a
			href={resolve('/')}
			class="flex items-center space-x-3 font-sans font-extrabold text-2xl tracking-wider text-amber-500"
		>
			<img src={logo} alt="BarberBase Logo" class="h-8 w-8" />
			<span>{BRAND}</span>
		</a>
		<a
			href={resolve('/')}
			class="text-sm font-semibold text-slate-400 hover:text-slate-200 transition-colors"
		>
			&larr; Back
		</a>
	</header>

	<main class="flex-grow relative z-10 w-full max-w-3xl mx-auto px-6 py-12 md:py-16 space-y-12">
		<!-- Page Header -->
		<div class="space-y-4">
			<h1 class="font-sans font-extrabold text-4xl md:text-5xl text-slate-100 tracking-tight">
				Terms of Service
			</h1>
			<p class="text-xs text-slate-500 font-mono">Last Updated: June 20, 2026</p>
		</div>

		<!-- Identity & Verification Block -->
		<section
			class="bg-slate-900/60 backdrop-blur-xl border border-slate-800/80 rounded-3xl p-6 md:p-8 space-y-4"
		>
			<h2 class="text-lg font-bold font-sans text-amber-500">
				Business Identity & Registration Details
			</h2>
			<div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-sm text-slate-300">
				<div>
					<span class="block text-[11px] uppercase tracking-wider text-slate-500 font-semibold"
						>Legal Entity</span
					>
					<span class="font-medium text-slate-200">{LEGAL_ENTITY_NAME}</span>
				</div>
				<div>
					<span class="block text-[11px] uppercase tracking-wider text-slate-500 font-semibold"
						>Udyam Registration No.</span
					>
					<span class="font-mono text-slate-200">{UDYAM_NUMBER}</span>
				</div>
				<div class="md:col-span-2">
					<span class="block text-[11px] uppercase tracking-wider text-slate-500 font-semibold"
						>Registered Address</span
					>
					<span class="text-slate-200">{REGISTERED_ADDRESS}</span>
				</div>
				<div>
					<span class="block text-[11px] uppercase tracking-wider text-slate-500 font-semibold"
						>Contact Email</span
					>
					<a
						href="mailto:{CONTACT_EMAIL}"
						class="text-amber-400 hover:text-amber-300 font-medium transition-colors"
						>{CONTACT_EMAIL}</a
					>
				</div>
				<div>
					<span class="block text-[11px] uppercase tracking-wider text-slate-500 font-semibold"
						>Contact Phone</span
					>
					<a
						href="tel:{CONTACT_PHONE.replace(/\s+/g, '')}"
						class="text-amber-400 hover:text-amber-300 font-medium transition-colors"
						>{CONTACT_PHONE}</a
					>
				</div>
			</div>
		</section>

		<!-- Content -->
		<article
			class="prose prose-invert prose-amber max-w-none space-y-8 text-slate-300 text-sm md:text-base leading-relaxed"
		>
			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">1. Acceptance of Terms</h3>
				<p>
					By checking into a virtual queue or logging into the staff/client portals of {BRAND} (referred
					to as the "Service"), which is operated by {LEGAL_ENTITY_NAME}, you agree to be bound by
					these Terms of Service. If you do not agree to these terms, you may not use the Service.
				</p>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">
					2. Authentication & Phone Identity
				</h3>
				<p>Our Service utilizes phone number authentication as a verification mechanism:</p>
				<ul class="list-disc pl-5 space-y-2 text-slate-400">
					<li>
						You represent that the phone number you provide belongs to you and is actively
						monitored.
					</li>
					<li>
						You consent to receive transactional and operational WhatsApp messages related to your
						queue check-ins. These messages are sent using our delivery wrapper, <span
							class="font-mono text-amber-400">Bhejna</span
						>.
					</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">
					3. Virtual Queue Etiquette & Guidelines
				</h3>
				<p>
					We provide virtual queuing to help streamline walk-ins at participating barbershops. Users
					agree to the following rules:
				</p>
				<ul class="list-disc pl-5 space-y-2 text-slate-400">
					<li>
						Queue positions are reserved for immediate personal use. Booking virtual slots using
						false identities or automated scripts is strictly prohibited.
					</li>
					<li>
						Participating barbershops maintain absolute authority to adjust queue order, delay
						slots, remove entries, or mark check-ins as "no-show" in accordance with their
						individual store policies.
					</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">4. Magic Link Security</h3>
				<p>
					Access to your active queue status page and dashboard is granted via short-lived <strong
						class="text-slate-200">23-hour magic links</strong
					>. You are solely responsible for:
				</p>
				<ul class="list-disc pl-5 space-y-2 text-slate-400">
					<li>
						Maintaining the confidentiality of these links. If you share your magic link, others may
						view your phone number and visit details.
					</li>
					<li>
						Requesting a new link should your session expire. The 23-hour limit is hard-coded to
						safeguard user security.
					</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">
					5. Service Fees & WhatsApp Notifications
				</h3>
				<p>
					{BRAND} does not charge customers directly to check into a virtual queue. However, message delivery
					is subject to network carrier conditions:
				</p>
				<ul class="list-disc pl-5 space-y-2 text-slate-400">
					<li>
						We do not guarantee instant or uninterrupted delivery of WhatsApp alerts. Delivery
						delays due to carrier network coverage or WhatsApp API limits are beyond our control.
					</li>
					<li>
						Standard data fees from your network provider may apply when viewing the virtual
						dashboard or receiving alerts.
					</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">6. Limitation of Liability</h3>
				<p>
					{LEGAL_ENTITY_NAME} provides {BRAND} on an "as is" and "as available" basis:
				</p>
				<ul class="list-disc pl-5 space-y-2 text-slate-400">
					<li>
						We do not guarantee the accuracy of estimated wait times displayed on the dashboard.
						They are estimates generated based on historical visit times and are subject to change.
					</li>
					<li>
						We are not liable for any disputes arising from the services performed at the physical
						barbershop, nor are we liable for any walk-in opportunities missed due to notification
						failures.
					</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">7. Governing Law</h3>
				<p>
					These Terms of Service shall be governed by and construed in accordance with the laws of
					India. Any disputes arising in connection with the Service shall be subject to the
					exclusive jurisdiction of the courts located in Thane/Mumbai, Maharashtra, India.
				</p>
			</section>
		</article>
	</main>

	<!-- Footer -->
	<SiteFooter />
</div>
