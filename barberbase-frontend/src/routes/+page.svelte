<script lang="ts">
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import { resolve } from '$app/paths';
	import logo from '$lib/assets/favicon.svg';
	import { BRAND, SALES_WHATSAPP, CONTACT_EMAIL } from '$lib/site-config';

	// Determine CTA URL
	const ctaUrl = SALES_WHATSAPP
		? `https://wa.me/${SALES_WHATSAPP}?text=Hi,%20I'm%20interested%20in%20BarberBase%20for%20my%20shop!`
		: `mailto:${CONTACT_EMAIL}?subject=Get%20BarberBase%20for%20my%20shop`;
</script>

<svelte:head>
	<title>{BRAND} — Smart Salon Queue & WhatsApp Updates</title>
</svelte:head>

<div
	class="min-h-screen bg-slate-950 text-slate-100 font-sans selection:bg-amber-500 selection:text-slate-950 flex flex-col relative overflow-hidden"
>
	<!-- Background Decorative Gradients -->
	<div class="absolute inset-0 overflow-hidden pointer-events-none z-0">
		<div
			class="absolute top-[-10%] left-[50%] -translate-x-1/2 w-[800px] h-[800px] rounded-full bg-amber-500/5 blur-[160px]"
		></div>
		<div
			class="absolute top-[40%] right-[-10%] w-[500px] h-[500px] rounded-full bg-amber-500/5 blur-[120px]"
		></div>
		<div
			class="absolute bottom-[20%] left-[-10%] w-[600px] h-[600px] rounded-full bg-blue-500/5 blur-[140px]"
		></div>
	</div>

	<!-- Navigation Bar -->
	<header
		class="w-full max-w-6xl mx-auto px-6 py-6 flex justify-between items-center relative z-10"
	>
		<div class="flex items-center space-x-3">
			<img src={logo} alt="BarberBase Logo" class="h-8 w-8" />
			<span class="font-sans font-extrabold text-2xl tracking-wider text-amber-500">BarberBase</span
			>
		</div>
		<div class="flex items-center space-x-6">
			<nav class="hidden md:flex space-x-6 text-sm font-medium text-slate-400">
				<a href={resolve('/about')} class="hover:text-slate-100 transition-colors">About</a>
				<a href={resolve('/contact')} class="hover:text-slate-100 transition-colors">Contact</a>
			</nav>
			<a
				href={resolve('/login')}
				class="px-5 py-2.5 bg-slate-900 border border-slate-800 rounded-xl text-sm font-semibold hover:bg-slate-800 hover:border-slate-700 transition-all"
			>
				Staff Login
			</a>
		</div>
	</header>

	<main class="flex-grow relative z-10 w-full max-w-4xl mx-auto px-6 py-12 md:py-20 space-y-24">
		<!-- Hero Section -->
		<section class="text-center space-y-6">
			<h1 class="font-sans font-extrabold text-5xl md:text-7xl tracking-tight leading-none">
				{BRAND}
			</h1>
			<p class="text-2xl md:text-3xl font-bold text-amber-500 font-sans">Never miss a customer.</p>
			<p class="text-lg md:text-xl text-slate-400 max-w-2xl mx-auto font-light leading-relaxed">
				Queue management and WhatsApp updates for barbershops.
			</p>

			<div class="pt-6">
				<a
					href={ctaUrl}
					class="inline-flex items-center justify-center px-8 py-4 bg-amber-500 hover:bg-amber-400 active:bg-amber-600 text-slate-950 font-bold text-lg rounded-2xl transition-all shadow-lg hover:shadow-amber-500/10 cursor-pointer"
				>
					Get BarberBase for your shop
				</a>
			</div>
		</section>

		<!-- Already in a queue Section -->
		<section class="max-w-2xl mx-auto">
			<div
				class="bg-amber-500/5 border border-amber-500/20 rounded-3xl p-6 md:p-8 space-y-3 relative overflow-hidden group hover:border-amber-500/30 transition-all duration-300"
			>
				<div
					class="absolute top-0 right-0 w-32 h-32 bg-amber-500/5 rounded-full blur-2xl group-hover:bg-amber-500/10 transition-all duration-300"
				></div>
				<div class="flex items-center space-x-3 text-amber-500">
					<svg
						xmlns="http://www.w3.org/2000/svg"
						class="h-6 w-6 shrink-0"
						fill="none"
						viewBox="0 0 24 24"
						stroke="currentColor"
					>
						<path
							stroke-linecap="round"
							stroke-linejoin="round"
							stroke-width="2"
							d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
						/>
					</svg>
					<h3 class="text-lg font-bold font-sans">Already in a queue?</h3>
				</div>
				<p class="text-slate-300 leading-relaxed text-sm md:text-base">
					Open the link your barbershop shared with you (<span class="font-mono text-amber-400"
						>barberbase.in/your-shop</span
					>). Don't have it? Ask the shop staff or scan their QR code at the counter.
				</p>
			</div>
		</section>

		<!-- "What it is" Section -->
		<section class="space-y-12">
			<div class="text-center space-y-2">
				<h2 class="font-sans font-extrabold text-3xl md:text-4xl text-slate-100">
					Frictionless Queue Management
				</h2>
				<p class="text-slate-400 text-sm md:text-base max-w-xl mx-auto">
					How BarberBase keeps your chairs full and your clients happy.
				</p>
			</div>

			<div class="grid grid-cols-1 md:grid-cols-3 gap-6">
				<!-- Card 1 -->
				<div
					class="bg-slate-900/50 backdrop-blur border border-slate-800/80 rounded-2xl p-6 space-y-4 hover:border-slate-700/80 transition-all duration-300"
				>
					<div
						class="w-12 h-12 bg-amber-500/10 rounded-xl flex items-center justify-center text-amber-500"
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
								d="M12 18h.01M8 21h8a2 2 0 002-2V5a2 2 0 00-2-2H8a2 2 0 00-2 2v14a2 2 0 002 2z"
							/>
						</svg>
					</div>
					<h3 class="text-lg font-bold font-sans text-slate-200">1. Join from Anywhere</h3>
					<p class="text-sm text-slate-400 leading-relaxed">
						Customers join the queue from their phone over WhatsApp. No app downloads required.
					</p>
				</div>

				<!-- Card 2 -->
				<div
					class="bg-slate-900/50 backdrop-blur border border-slate-800/80 rounded-2xl p-6 space-y-4 hover:border-slate-700/80 transition-all duration-300"
				>
					<div
						class="w-12 h-12 bg-amber-500/10 rounded-xl flex items-center justify-center text-amber-500"
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
								d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9"
							/>
						</svg>
					</div>
					<h3 class="text-lg font-bold font-sans text-slate-200">2. Real-Time Tracking</h3>
					<p class="text-sm text-slate-400 leading-relaxed">
						Clients monitor their live position on a personal web screen and receive an automated
						WhatsApp notification when their turn is near.
					</p>
				</div>

				<!-- Card 3 -->
				<div
					class="bg-slate-900/50 backdrop-blur border border-slate-800/80 rounded-2xl p-6 space-y-4 hover:border-slate-700/80 transition-all duration-300"
				>
					<div
						class="w-12 h-12 bg-amber-500/10 rounded-xl flex items-center justify-center text-amber-500"
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
								d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 002 2h2a2 2 0 002-2z"
							/>
						</svg>
					</div>
					<h3 class="text-lg font-bold font-sans text-slate-200">3. Stop Losing Clients</h3>
					<p class="text-sm text-slate-400 leading-relaxed">
						Shops prevent walk-in drops, clear overcrowding in waiting areas, and build a database
						of customer history automatically.
					</p>
				</div>
			</div>
		</section>

		<!-- Teaser About Section -->
		<section
			class="bg-slate-900/40 border border-slate-900 rounded-3xl p-8 md:p-12 flex flex-col md:flex-row justify-between items-center gap-8"
		>
			<div class="space-y-3 text-center md:text-left max-w-md">
				<h3 class="font-sans font-bold text-2xl text-slate-100">Our Mission</h3>
				<p class="text-sm text-slate-400 leading-relaxed">
					BarberBase is built to make waiting at the barbershop a thing of the past. We bridge the
					gap between barbers and their clients with simple, real-time queues.
				</p>
			</div>
			<a
				href={resolve('/about')}
				class="px-6 py-3 bg-slate-900 border border-slate-800 rounded-xl font-semibold text-sm hover:bg-slate-800 hover:border-slate-700 hover:text-amber-500 transition-all duration-200 shrink-0"
			>
				Learn More &rarr;
			</a>
		</section>
	</main>

	<!-- Footer -->
	<SiteFooter />
</div>
