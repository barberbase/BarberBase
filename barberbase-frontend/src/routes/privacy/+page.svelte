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
	<title>Privacy Policy — {BRAND}</title>
</svelte:head>

<div
	class="min-h-screen bg-slate-950 text-slate-100 font-sans selection:bg-amber-500 selection:text-slate-950 flex flex-col relative overflow-hidden"
>
	<!-- Background Decorative Gradients -->
	<div class="absolute inset-0 overflow-hidden pointer-events-none z-0">
		<div
			class="absolute top-[-10%] right-[10%] w-[600px] h-[600px] rounded-full bg-amber-500/5 blur-[130px]"
		></div>
		<div
			class="absolute bottom-[10%] left-[10%] w-[500px] h-[500px] rounded-full bg-blue-500/5 blur-[120px]"
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
				Privacy Policy
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
				<h3 class="text-xl font-bold font-sans text-slate-100">1. Information We Collect</h3>
				<p>
					To provide virtual queueing and real-time updates for customers visiting our partner
					barbershops, {BRAND} collects the minimum necessary data to manage active customer visits. We
					collect:
				</p>
				<ul class="list-disc pl-5 space-y-2 text-slate-400">
					<li>
						<strong class="text-slate-200">Phone Number:</strong> Used as your unique identity. When you
						check into a queue or log in to your dashboard, we verify your identity via a one-time passcode
						sent over WhatsApp.
					</li>
					<li>
						<strong class="text-slate-200">Queue & Visit History:</strong> Information about your active
						and past queue check-ins, including which barbershop you joined, the services you selected,
						your checked-in timestamp, and your queue status.
					</li>
					<li>
						<strong class="text-slate-200">Technical Device Data:</strong> IP address, browser type, and
						Server-Sent Events (SSE) log details used strictly to maintain live, real-time connection
						streams that display your active queue position.
					</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">2. How We Use Your Data</h3>
				<p>
					We use the collected information solely for operating and optimizing the queue service:
				</p>
				<ul class="list-disc pl-5 space-y-2 text-slate-400">
					<li>
						<strong class="text-slate-200">Queue Operations:</strong> Allowing barbershops to view their
						list of waiting customers, update queue statuses, and select customers when it is their turn.
					</li>
					<li>
						<strong class="text-slate-200">Notification Delivery:</strong> Sending you queue updates
						and "your-turn" alerts. These messages are delivered to your phone over WhatsApp via our
						integrated messaging system,
						<span class="text-amber-400 font-semibold font-mono">Bhejna</span>.
					</li>
					<li>
						<strong class="text-slate-200">Secure Access:</strong> To protect your personal queue
						dashboard, we generate short-lived, secure
						<strong class="text-slate-200">23-hour magic-link tokens</strong>. These links ensure
						that your session automatically expires, protecting your phone number and visit details.
					</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">3. Data Sharing & Third Parties</h3>
				<p>
					Your privacy is highly valued. We do not sell, rent, or trade your personal data. Your
					data is shared only under the following limited circumstances:
				</p>
				<ul class="list-disc pl-5 space-y-2 text-slate-400">
					<li>
						<strong class="text-slate-200">Partner Barbershops:</strong> When you join a specific shop's
						queue, the staff of that shop can view your name, phone number, and selected services to administer
						your booking.
					</li>
					<li>
						<strong class="text-slate-200">WhatsApp Notification Providers:</strong> Phone numbers and
						status alerts are routed through the WhatsApp Business API using our Bhejna delivery framework
						to successfully send notifications.
					</li>
				</ul>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">4. Data Retention</h3>
				<p>
					We do not keep active queue data longer than necessary. Customer queue entries are
					maintained active during the day of the visit. Once a visit is marked completed,
					cancelled, or no-show, the personal details are archived and only anonymized metrics (such
					as average wait times or customer volume) are kept for analytical purposes.
				</p>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">5. Security</h3>
				<p>
					We implement industry-standard technical measures to safeguard your information. SvelteKit
					route protection and secure backend API tokens prevent unauthorized access. The 23-hour
					magic-link expiration policy ensures that even if a link is shared, access to personal
					dashboards is strictly short-lived.
				</p>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">6. Your Rights</h3>
				<p>
					You have the right to request access to the phone number and visit logs associated with
					your identity, or request the deletion of your historical visit information. To exercise
					these rights, please contact us at <a
						href="mailto:{CONTACT_EMAIL}"
						class="text-amber-400 hover:underline">{CONTACT_EMAIL}</a
					>.
				</p>
			</section>
		</article>
	</main>

	<!-- Footer -->
	<SiteFooter />
</div>
