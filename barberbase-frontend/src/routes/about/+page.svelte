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
	<title>About Us — {BRAND}</title>
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
				About Us
			</h1>
			<p class="text-lg text-slate-400 font-light leading-relaxed">
				Frictionless, real-time virtual queueing built specifically for modern barbershops.
			</p>
		</div>

		<!-- Main Statement Card -->
		<section
			class="bg-amber-500/5 border border-amber-500/20 rounded-3xl p-6 md:p-8 space-y-3 relative overflow-hidden"
		>
			<div class="absolute top-0 right-0 w-32 h-32 bg-amber-500/5 rounded-full blur-2xl"></div>
			<p class="text-slate-200 text-base md:text-lg leading-relaxed font-medium">
				BarberBase is a registered unit of CodeNXT Lab (Udyam UDYAM-MH-33-0763915), based in
				Bhayandar, Thane, Maharashtra.
			</p>
		</section>

		<!-- Story Content -->
		<article
			class="prose prose-invert prose-amber max-w-none space-y-8 text-slate-300 text-sm md:text-base leading-relaxed"
		>
			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">Our Vision</h3>
				<p>
					We believe that waiting in line at the barbershop should not consume your day. Standard
					booking calendars are often too rigid for the dynamic flow of walk-in salons, leading to
					empty chairs during gaps and overcrowded waiting rooms during peaks.
				</p>
				<p>
					{BRAND} was created to fill this exact gap: bridging physical barbershop walk-ins with virtual,
					real-time queues. By allowing customers to join the queue directly from WhatsApp, track their
					live position on their phones, and receive a automated notification when they are next, we restore
					flexibility to both clients and shop staff.
				</p>
			</section>

			<section class="space-y-3">
				<h3 class="text-xl font-bold font-sans text-slate-100">How We Operate</h3>
				<p>
					As a product of {LEGAL_ENTITY_NAME}, {BRAND} is engineered to be extremely lightweight, secure,
					and intuitive.
				</p>
				<ul class="list-disc pl-5 space-y-2 text-slate-400">
					<li>
						<strong class="text-slate-200">Phone-First Convenience:</strong> No customer usernames or
						password management. Customer authentication runs cleanly via mobile phone numbers, backed
						by secure 23-hour session magic links.
					</li>
					<li>
						<strong class="text-slate-200">Bhejna WhatsApp Pipeline:</strong> Fast status notification
						delivery to ensure customers never miss their turns and barbershops keep their work schedules
						running smoothly.
					</li>
					<li>
						<strong class="text-slate-200">Privacy by Design:</strong> Session links expire automatically,
						and visit history data is archived day-of, ensuring user details are never kept indefinitely.
					</li>
				</ul>
			</section>
		</article>

		<!-- Identity & Verification Block -->
		<section
			class="bg-slate-900/60 backdrop-blur-xl border border-slate-800/80 rounded-3xl p-6 md:p-8 space-y-4"
		>
			<h2 class="text-lg font-bold font-sans text-amber-500">Business Registration Information</h2>
			<div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-sm text-slate-300">
				<div>
					<span class="block text-[11px] uppercase tracking-wider text-slate-500 font-semibold"
						>Legal Entity Name</span
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
	</main>

	<!-- Footer -->
	<SiteFooter />
</div>
