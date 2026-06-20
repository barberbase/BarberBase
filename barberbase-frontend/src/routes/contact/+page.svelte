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
	<title>Contact Us — {BRAND}</title>
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
				Contact Us
			</h1>
			<p class="text-lg text-slate-400 font-light leading-relaxed">
				Have questions about BarberBase? Get in touch with our team.
			</p>
		</div>

		<!-- Direct Action Cards -->
		<section class="grid grid-cols-1 md:grid-cols-2 gap-6">
			<!-- Email Card -->
			<a
				href="mailto:{CONTACT_EMAIL}"
				class="bg-slate-900/50 backdrop-blur border border-slate-800/80 rounded-3xl p-6 md:p-8 flex items-start space-x-5 hover:border-amber-500/40 hover:bg-slate-900/70 transition-all duration-300 group"
			>
				<div
					class="w-12 h-12 bg-amber-500/10 rounded-2xl flex items-center justify-center text-amber-500 shrink-0 group-hover:bg-amber-500/20 transition-all"
				>
					<svg
						xmlns="http://www.w3.org/2000/svg"
						class="h-6 w-6"
						fill="none"
						viewBox="0 0 24 24"
						stroke="currentColor"
					>
						<path
							stroke-linecap="round"
							stroke-linejoin="round"
							stroke-width="2"
							d="M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"
						/>
					</svg>
				</div>
				<div class="space-y-1">
					<span class="block text-[11px] uppercase tracking-wider text-slate-500 font-semibold"
						>Email Support</span
					>
					<span
						class="block text-lg font-bold font-sans text-slate-200 group-hover:text-amber-500 transition-colors"
						>{CONTACT_EMAIL}</span
					>
					<span class="block text-xs text-slate-400"
						>For general inquiries, sales, and support.</span
					>
				</div>
			</a>

			<!-- Phone Card -->
			<a
				href="tel:{CONTACT_PHONE.replace(/\s+/g, '')}"
				class="bg-slate-900/50 backdrop-blur border border-slate-800/80 rounded-3xl p-6 md:p-8 flex items-start space-x-5 hover:border-amber-500/40 hover:bg-slate-900/70 transition-all duration-300 group"
			>
				<div
					class="w-12 h-12 bg-amber-500/10 rounded-2xl flex items-center justify-center text-amber-500 shrink-0 group-hover:bg-amber-500/20 transition-all"
				>
					<svg
						xmlns="http://www.w3.org/2000/svg"
						class="h-6 w-6"
						fill="none"
						viewBox="0 0 24 24"
						stroke="currentColor"
					>
						<path
							stroke-linecap="round"
							stroke-linejoin="round"
							stroke-width="2"
							d="M3 5a2 2 0 012-2h3.28a1 1 0 01.94.725l.548 2.2a1 1 0 01-.321.988l-1.305.98a10.582 10.582 0 004.872 4.872l.98-1.305a1 1 0 01.988-.321l2.2.548a1 1 0 01.725.94V19a2 2 0 01-2 2h-1C9.716 21 3 14.284 3 6V5z"
						/>
					</svg>
				</div>
				<div class="space-y-1">
					<span class="block text-[11px] uppercase tracking-wider text-slate-500 font-semibold"
						>Call or WhatsApp</span
					>
					<span
						class="block text-lg font-bold font-sans text-slate-200 group-hover:text-amber-500 transition-colors"
						>{CONTACT_PHONE}</span
					>
					<span class="block text-xs text-slate-400">Available Mon-Sat, 10 AM to 7 PM IST.</span>
				</div>
			</a>
		</section>

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
