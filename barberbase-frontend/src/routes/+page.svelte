<script lang="ts">
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import { resolve } from '$app/paths';
	import logo from '$lib/assets/favicon.svg';
	import { BRAND, SALES_WHATSAPP, CONTACT_EMAIL } from '$lib/site-config';
	import Card from '$lib/components/Card.svelte';
	import Button from '$lib/components/Button.svelte';

	// Determine CTA URL
	const ctaUrl = SALES_WHATSAPP
		? `https://wa.me/${SALES_WHATSAPP}?text=Hi,%20I'm%20interested%20in%20BarberBase%20for%20my%20shop!`
		: `mailto:${CONTACT_EMAIL}?subject=Get%20BarberBase%20for%20my%20shop`;
</script>

<svelte:head>
	<title>{BRAND} — Smart Salon Queue & WhatsApp Updates</title>
</svelte:head>

<div
	class="min-h-screen bg-canvas text-primary font-manrope flex flex-col relative overflow-hidden"
>
	<!-- Background Decorative Gradients -->
	<div class="absolute inset-0 overflow-hidden pointer-events-none z-0">
		<div
			class="absolute top-[-10%] left-[50%] -translate-x-1/2 w-[800px] h-[800px] rounded-full bg-gold-accent/5 blur-[160px]"
		></div>
		<div
			class="absolute top-[40%] right-[-10%] w-[500px] h-[500px] rounded-full bg-gold-accent/5 blur-[120px]"
		></div>
		<div
			class="absolute bottom-[20%] left-[-10%] w-[600px] h-[600px] rounded-full bg-white/5 blur-[140px]"
		></div>
	</div>

	<!-- Navigation Bar -->
	<header
		class="w-full max-w-6xl mx-auto px-6 py-6 flex justify-between items-center relative z-10"
	>
		<div class="flex items-center space-x-3">
			<img src={logo} alt="BarberBase Logo" class="h-8 w-8" />
			<span class="font-satoshi font-extrabold text-2xl tracking-widestUI text-gold-accent">BarberBase</span>
		</div>
		<div class="flex items-center space-x-6">
			<nav class="hidden md:flex space-x-6 text-sm font-medium text-muted">
				<a href={resolve('/about')} class="hover:text-primary transition-colors">About</a>
				<a href={resolve('/contact')} class="hover:text-primary transition-colors">Contact</a>
			</nav>
			<a href={resolve('/login')} class="w-32 inline-block">
				<Button variant="secondary" class="py-2.5 px-5 text-[11px] font-semibold border-white/10">Staff Login</Button>
			</a>
		</div>
	</header>

	<main class="flex-grow relative z-10 w-full max-w-4xl mx-auto px-6 py-12 md:py-20 space-y-24">
		<!-- Hero Section -->
		<section class="text-center space-y-6">
			<h1 class="font-satoshi font-extrabold text-5xl md:text-7xl tracking-tightest leading-none text-primary">
				{BRAND}
			</h1>
			<p class="text-2xl md:text-3xl font-bold text-gold-accent font-satoshi">Never miss a customer.</p>
			<p class="text-lg md:text-xl text-muted max-w-2xl mx-auto font-light leading-relaxed font-manrope">
				Queue management and WhatsApp updates for barbershops.
			</p>

			<div class="pt-6 flex justify-center">
				<a href={ctaUrl} class="w-full max-w-sm block">
					<Button variant="primary">Get BarberBase for your shop</Button>
				</a>
			</div>
		</section>

		<!-- Already in a queue Section -->
		<section class="max-w-2xl mx-auto w-full">
			<Card variant="surface" class="border-gold-accent/20 space-y-3 group hover:border-gold-accent/30 transition-all duration-300">
				<div
					class="absolute top-0 right-0 w-32 h-32 bg-gold-accent/5 rounded-full blur-2xl group-hover:bg-gold-accent/10 transition-all duration-300"
				></div>
				<div class="flex items-center space-x-3 text-gold-accent">
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
					<h3 class="text-lg font-bold font-satoshi">Already in a queue?</h3>
				</div>
				<p class="text-primary leading-relaxed text-sm md:text-base font-manrope">
					Open the link your barbershop shared with you (<span class="font-mono text-gold-accent"
						>barberbase.in/your-shop</span
					>). Don't have it? Ask the shop staff or scan their QR code at the counter.
				</p>
			</Card>
		</section>

		<!-- "What it is" Section -->
		<section class="space-y-12">
			<div class="text-center space-y-2">
				<h2 class="font-satoshi font-extrabold text-3xl md:text-4xl text-primary">
					Frictionless Queue Management
				</h2>
				<p class="text-muted text-sm md:text-base max-w-xl mx-auto font-manrope">
					How BarberBase keeps your chairs full and your clients happy.
				</p>
			</div>

			<div class="grid grid-cols-1 md:grid-cols-3 gap-6">
				<!-- Card 1 -->
				<Card variant="matte" class="space-y-4 hover:border-white/[0.06] transition-all duration-300">
					<div
						class="w-12 h-12 bg-gold-accent/10 rounded-xl flex items-center justify-center text-gold-accent"
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
					<h3 class="text-lg font-bold font-satoshi text-primary">1. Join from Anywhere</h3>
					<p class="text-sm text-muted leading-relaxed font-manrope">
						Customers join the queue from their phone over WhatsApp. No app downloads required.
					</p>
				</Card>

				<!-- Card 2 -->
				<Card variant="matte" class="space-y-4 hover:border-white/[0.06] transition-all duration-300">
					<div
						class="w-12 h-12 bg-gold-accent/10 rounded-xl flex items-center justify-center text-gold-accent"
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
					<h3 class="text-lg font-bold font-satoshi text-primary">2. Real-Time Tracking</h3>
					<p class="text-sm text-muted leading-relaxed font-manrope">
						Clients monitor their live position on a personal web screen and receive an automated
						WhatsApp notification when their turn is near.
					</p>
				</Card>

				<!-- Card 3 -->
				<Card variant="matte" class="space-y-4 hover:border-white/[0.06] transition-all duration-300">
					<div
						class="w-12 h-12 bg-gold-accent/10 rounded-xl flex items-center justify-center text-gold-accent"
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
					<h3 class="text-lg font-bold font-satoshi text-primary">3. Stop Losing Clients</h3>
					<p class="text-sm text-muted leading-relaxed font-manrope">
						Shops prevent walk-in drops, clear overcrowding in waiting areas, and build a database
						of customer history automatically.
					</p>
				</Card>
			</div>
		</section>

		<!-- Teaser About Section -->
		<section class="w-full">
			<Card variant="surface" class="flex flex-col md:flex-row justify-between items-center gap-8 text-center md:text-left">
				<div class="space-y-3 max-w-md">
					<h3 class="font-satoshi font-bold text-2xl text-primary">Our Mission</h3>
					<p class="text-sm text-muted leading-relaxed font-manrope">
						BarberBase is built to make waiting at the barbershop a thing of the past. We bridge the
						gap between barbers and their clients with simple, real-time queues.
					</p>
				</div>
				<a href={resolve('/about')} class="shrink-0 w-full md:w-auto block">
					<Button variant="secondary">Learn More &rarr;</Button>
				</a>
			</Card>
		</section>
	</main>

	<!-- Footer -->
	<SiteFooter />
</div>
