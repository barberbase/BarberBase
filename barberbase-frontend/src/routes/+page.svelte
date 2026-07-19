<script lang="ts">
	import { onMount } from 'svelte';
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import Seo from '$lib/components/Seo.svelte';
	import { resolve } from '$app/paths';
	import {
		BRAND,
		SALES_WHATSAPP,
		CONTACT_EMAIL,
		LEGAL_ENTITY_NAME,
		UDYAM_NUMBER,
		SITE_URL,
		ORGANIZATION_JSON_LD
	} from '$lib/site-config';
	import Button from '$lib/components/Button.svelte';

	// Animation: default visible, JS adds the animate-ready class then sequences
	let animateReady = $state(false);
	let heroReady = $state(false);
	let phoneReady = $state(false);
	let bubblesShown = $state(0);

	onMount(() => {
		const prefersReduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
		if (prefersReduced) {
			heroReady = true; phoneReady = true; bubblesShown = 4;
			return;
		}
		animateReady = true;
		requestAnimationFrame(() => {
			setTimeout(() => heroReady = true, 100);
			setTimeout(() => phoneReady = true, 500);
			setTimeout(() => bubblesShown = 1, 900);
			setTimeout(() => bubblesShown = 2, 1350);
			setTimeout(() => bubblesShown = 3, 1800);
			setTimeout(() => bubblesShown = 4, 2200);
		});
	});

	const ctaUrl = SALES_WHATSAPP
		? `https://wa.me/${SALES_WHATSAPP}?text=Hi,%20I'm%20interested%20in%20BarberBase%20for%20my%20shop!`
		: `mailto:${CONTACT_EMAIL}?subject=Get%20BarberBase%20for%20my%20shop`;

	const faqs = [
		{ q: 'Is BarberBase a real company? Who runs it?', a: `Yes. BarberBase is built and operated by ${LEGAL_ENTITY_NAME} (Udyam ${UDYAM_NUMBER}).`, link: { href: '/about', label: 'See our full registered-entity details and contact info' } },
		{ q: 'How does WhatsApp queue joining work?', a: 'Scan a QR code or tap a link to join a shop\'s queue — no app or account needed. You see your live position and estimated wait as they update, and get a WhatsApp message when your turn is near.' },
		{ q: 'Do I need to download an app?', a: 'No. Joining a queue or booking an appointment works entirely in your phone\'s browser and WhatsApp — no app install, no account, no password.' },
		{ q: 'Is my number and data safe?', a: 'We use your WhatsApp number only to send queue and appointment updates. Access links expire, and we don\'t ask for passwords or accounts.' },
		{ q: 'How do I book an appointment?', a: 'Pick a service and time slot on the shop\'s page — you get a WhatsApp confirmation. No account and no back-and-forth calls.' },
		{ q: 'Is there a cost to join the queue?', a: 'No. Joining a queue or booking an appointment is free for customers.' },
		{ q: 'What happens if I miss my turn?', a: 'If you don\'t confirm you\'re on your way in time, the shop can release your spot to the next customer in line.' },
		{ q: 'How is this different from just calling the shop?', a: 'A phone call only tells you your position at that moment, and ties up the shop\'s line. BarberBase shows your live position and estimated wait continuously, and messages you on WhatsApp when it\'s time — no calling back to check.' },
		{ q: 'How much does it cost?', a: 'We\'re in early access right now. Chat with us on WhatsApp and we\'ll set you up — pricing depends on shop size.' },
		{ q: 'What if I have multiple branches?', a: 'Each location gets its own queue and dashboard. Staff only see their shop, you see everything.' },
		{ q: 'How long does setup take?', a: 'Under 10 minutes. We add your services, generate your QR, and you\'re live. No hardware needed.' },
		{ q: 'Does the shop need special equipment or an app?', a: 'No. Staff run everything from the dashboard in a browser — no hardware, no app install for staff or customers.' }
	];

	const faqJsonLd = {
		'@context': 'https://schema.org',
		'@type': 'FAQPage',
		mainEntity: faqs.map((f) => ({
			'@type': 'Question',
			name: f.q,
			acceptedAnswer: { '@type': 'Answer', text: f.a }
		}))
	};

	const speakableJsonLd = {
		'@context': 'https://schema.org',
		'@type': 'WebPage',
		url: SITE_URL,
		speakable: {
			'@type': 'SpeakableSpecification',
			cssSelector: ['#faq-heading', '.faq-item']
		}
	};

	let openFaq = $state(-1);
</script>

<Seo
	title="{BRAND} — Never Miss a Customer"
	description="Real-time walk-in queue and appointment booking with automatic WhatsApp turn alerts for barbershops in Mumbai."
	path="/"
/>

<svelte:head>
	{@html `<script type="application/ld+json">${JSON.stringify(ORGANIZATION_JSON_LD).replace(/</g, '\\u003c')}</script>`}
	{@html `<script type="application/ld+json">${JSON.stringify(faqJsonLd).replace(/</g, '\\u003c')}</script>`}
	{@html `<script type="application/ld+json">${JSON.stringify(speakableJsonLd).replace(/</g, '\\u003c')}</script>`}
</svelte:head>

<div class="min-h-screen bg-canvas text-primary font-manrope flex flex-col">
	<SiteHeader />

	<main id="main-content" class="flex-grow">
		<!-- Hero -->
		<section class="relative w-full max-w-6xl mx-auto px-6 pt-16 pb-20 md:pt-28 md:pb-32">
			<div class="absolute inset-0 pointer-events-none" aria-hidden="true">
				<div class="hero-glow absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[600px] h-[600px] rounded-full bg-gold-accent/[0.04] blur-[140px]"></div>
			</div>
			<div class="relative grid grid-cols-1 md:grid-cols-2 gap-12 md:gap-16 items-center">
				<div class="space-y-6 text-center md:text-left" class:hero-text={animateReady} class:hero-visible={heroReady}>
					<h1 class="font-satoshi font-extrabold text-[2.25rem] sm:text-[2.75rem] md:text-[3.75rem] tracking-[-0.035em] leading-[1.06]" style="text-wrap: balance;">
						Never miss a customer.
					</h1>
					<p class="text-lg md:text-xl text-muted max-w-lg leading-relaxed" style="text-wrap: pretty;">
						Walk-in queue management with automatic WhatsApp turn alerts. Customers join from a link, staff runs one board, nobody waits blindly.
					</p>
					<div class="pt-1 flex flex-col sm:flex-row items-center md:items-start gap-3">
						<a href={ctaUrl} class="w-full sm:w-auto sm:min-w-56 block">
							<Button variant="primary">Chat with us on WhatsApp</Button>
						</a>
						<a href={resolve('/demo')} class="w-full sm:w-auto sm:min-w-44 block">
							<Button variant="secondary">See live demo</Button>
						</a>
					</div>
				</div>

				<!-- WhatsApp mockup — sequenced after hero text -->
				<div class="flex justify-center md:justify-end" class:phone-entrance={animateReady} class:phone-visible={phoneReady} aria-hidden="true">
					<div class="wa-phone w-full max-w-[280px] rounded-2xl bg-[#0B141A] border border-white/[0.06] overflow-hidden shadow-[0_8px_40px_rgba(0,0,0,0.5)]">
						<div class="flex items-center gap-2.5 px-4 py-3 bg-[#1F2C33] border-b border-white/[0.04]">
							<div class="w-8 h-8 rounded-full bg-[#25D366]/20 flex items-center justify-center shrink-0">
								<svg width="16" height="16" viewBox="0 0 24 24" fill="#25D366"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347z"/><path d="M12 0C5.373 0 0 5.373 0 12c0 2.625.846 5.059 2.284 7.034L.789 23.492a.5.5 0 00.611.611l4.458-1.495A11.943 11.943 0 0012 24c6.627 0 12-5.373 12-12S18.627 0 12 0zm0 22c-2.29 0-4.403-.764-6.1-2.052l-.426-.33-2.822.946.946-2.822-.33-.426A9.935 9.935 0 012 12C2 6.477 6.477 2 12 2s10 4.477 10 10-4.477 10-10 10z"/></svg>
							</div>
							<div>
								<p class="text-[13px] font-semibold text-[#E9EDEF]">BarberBase</p>
								<p class="text-[10px] text-[#8696A0]">Business account</p>
							</div>
						</div>

						<div class="px-3 py-4 space-y-2.5 min-h-[280px] bg-[#0B141A]">
							<div class="wa-bubble wa-bubble-in" class:bubble-enter={animateReady} class:bubble-visible={bubblesShown >= 1}>
								<p class="text-[12px] leading-[1.45] text-[#E9EDEF]">
									Hi Rahul! You're <span class="font-semibold text-[#25D366]">#3</span> in queue at <span class="font-semibold">Ravi's Cuts, Andheri</span>.
								</p>
								<p class="text-[12px] leading-[1.45] text-[#E9EDEF] mt-1">Est. wait: ~12 min</p>
								<span class="wa-time">10:42 AM</span>
							</div>

							<div class="wa-bubble wa-bubble-in" class:bubble-enter={animateReady} class:bubble-visible={bubblesShown >= 2}>
								<p class="text-[12px] leading-[1.45] text-[#E9EDEF]">
									You moved up! Now <span class="font-semibold text-[#25D366]">#1</span> in queue.
								</p>
								<span class="wa-time">10:51 AM</span>
							</div>

							<div class="wa-bubble wa-bubble-in wa-bubble-gold" class:bubble-enter={animateReady} class:bubble-visible={bubblesShown >= 3} class:gold-pulse={bubblesShown >= 3}>
								<p class="text-[12px] leading-[1.45] text-[#E9EDEF] font-semibold">
									It's your turn! Head to the chair now.
								</p>
								<span class="wa-time">10:54 AM</span>
							</div>

							<div class="wa-bubble wa-bubble-out" class:bubble-enter={animateReady} class:bubble-visible={bubblesShown >= 4}>
								<p class="text-[12px] leading-[1.45] text-[#E9EDEF]">Coming in 2 min!</p>
								<span class="wa-time">10:54 AM</span>
							</div>
						</div>
					</div>
				</div>
			</div>
		</section>

		<!-- How it works -->
		<section class="w-full max-w-5xl mx-auto px-6 pt-10 pb-20 md:pb-28">
			<h2 class="font-satoshi font-bold text-2xl md:text-3xl tracking-[-0.02em] text-center mb-14" style="text-wrap: balance;">
				Three steps. Zero confusion.
			</h2>
			<div class="grid grid-cols-1 md:grid-cols-3 gap-px bg-white/[0.03] rounded-xl overflow-hidden">
				<div class="bg-canvas p-7 md:p-8 space-y-4 step-cell">
					<div class="step-visual" aria-hidden="true">
						<div class="w-[72px] h-[100px] rounded-lg bg-matte border border-white/[0.06] mx-auto flex flex-col items-center justify-center gap-1.5 overflow-hidden">
							<div class="grid grid-cols-3 grid-rows-3 gap-[2px] w-7 h-7">
								<div class="bg-primary/60 rounded-[1px]"></div><div class="bg-primary/20 rounded-[1px]"></div><div class="bg-primary/60 rounded-[1px]"></div>
								<div class="bg-primary/20 rounded-[1px]"></div><div class="bg-primary/60 rounded-[1px]"></div><div class="bg-primary/20 rounded-[1px]"></div>
								<div class="bg-primary/60 rounded-[1px]"></div><div class="bg-primary/20 rounded-[1px]"></div><div class="bg-primary/60 rounded-[1px]"></div>
							</div>
							<span class="text-[7px] text-muted font-mono">scan to join</span>
						</div>
					</div>
					<span class="font-mono text-gold-accent/30 text-sm tracking-widestUI select-none">1</span>
					<h3 class="font-satoshi font-bold text-base">Share your link</h3>
					<p class="text-sm text-muted leading-relaxed">Customers scan a QR or tap a link — joined in under 5 seconds, no app download.</p>
				</div>

				<div class="bg-canvas p-7 md:p-8 space-y-4 step-cell">
					<div class="step-visual" aria-hidden="true">
						<div class="w-[120px] rounded-lg bg-matte border border-white/[0.06] mx-auto p-2 space-y-1.5 overflow-hidden">
							<div class="flex items-center gap-1.5">
								<div class="w-1.5 h-1.5 rounded-full bg-gold-accent"></div>
								<div class="h-1.5 w-12 bg-primary/20 rounded-full"></div>
								<span class="text-[7px] text-gold-accent font-mono ml-auto">NOW</span>
							</div>
							<div class="flex items-center gap-1.5">
								<div class="w-1.5 h-1.5 rounded-full bg-system-success/60"></div>
								<div class="h-1.5 w-10 bg-primary/15 rounded-full"></div>
								<span class="text-[7px] text-muted font-mono ml-auto">~8m</span>
							</div>
							<div class="flex items-center gap-1.5">
								<div class="w-1.5 h-1.5 rounded-full bg-dim/40"></div>
								<div class="h-1.5 w-14 bg-primary/10 rounded-full"></div>
								<span class="text-[7px] text-muted font-mono ml-auto">~16m</span>
							</div>
						</div>
					</div>
					<span class="font-mono text-gold-accent/30 text-sm tracking-widestUI select-none">2</span>
					<h3 class="font-satoshi font-bold text-base">Staff runs the board</h3>
					<p class="text-sm text-muted leading-relaxed">One live dashboard for walk-ins, appointments, and checkouts. Drag, tap, done.</p>
				</div>

				<div class="bg-canvas p-7 md:p-8 space-y-4 step-cell">
					<div class="step-visual" aria-hidden="true">
						<div class="w-[130px] mx-auto space-y-1">
							<div class="bg-[#1F2C33] rounded-lg rounded-tl-sm px-2.5 py-1.5">
								<p class="text-[8px] text-[#25D366] font-semibold">BarberBase</p>
								<p class="text-[8px] text-[#E9EDEF] leading-[1.4] mt-0.5">It's your turn! Head to the chair now.</p>
								<p class="text-[6px] text-[#8696A0] text-right mt-0.5">10:54 AM</p>
							</div>
							<div class="bg-[#005C4B] rounded-lg rounded-tr-sm px-2.5 py-1.5 ml-auto w-fit">
								<p class="text-[8px] text-[#E9EDEF]">On my way!</p>
								<p class="text-[6px] text-[#8696A0] text-right mt-0.5">10:54 AM</p>
							</div>
						</div>
					</div>
					<span class="font-mono text-gold-accent/30 text-sm tracking-widestUI select-none">3</span>
					<h3 class="font-satoshi font-bold text-base">WhatsApp does the rest</h3>
					<p class="text-sm text-muted leading-relaxed">Turn alerts go out automatically. Customers show up on time, chairs stay full.</p>
				</div>
			</div>
		</section>

		<!-- Features -->
		<section class="w-full max-w-5xl mx-auto px-6 pb-20 md:pb-28">
			<div class="bg-matte border border-white/[0.03] rounded-2xl machined-edge p-8 md:p-12">
				<h2 class="font-satoshi font-bold text-2xl md:text-3xl tracking-[-0.02em] mb-3" style="text-wrap: balance;">
					Everything your shop needs.
				</h2>
				<p class="text-muted mb-10 max-w-lg">No bloat. Just the tools that keep chairs occupied and customers happy.</p>

				<div class="grid grid-cols-1 md:grid-cols-2 gap-x-12 gap-y-8">
					<div class="space-y-2">
						<div class="flex items-center gap-2.5">
							<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" class="text-gold-accent shrink-0"><path d="M4.9 19.1C1 15.2 1 8.8 4.9 4.9"/><path d="M7.8 16.2a6 6 0 010-8.4"/><circle cx="12" cy="12" r="2"/><path d="M16.2 7.8a6 6 0 010 8.4"/><path d="M19.1 4.9C23 8.8 23 15.2 19.1 19.1"/></svg>
							<h3 class="font-satoshi font-semibold text-sm text-primary">Real-time queue</h3>
						</div>
						<p class="text-sm text-muted leading-relaxed max-w-[50ch]">Live position updates — no refresh, no guessing. Customers see exactly where they stand.</p>
					</div>
					<div class="space-y-2">
						<div class="flex items-center gap-2.5">
							<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" class="text-gold-accent shrink-0"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/></svg>
							<h3 class="font-satoshi font-semibold text-sm text-primary">WhatsApp alerts</h3>
						</div>
						<p class="text-sm text-muted leading-relaxed max-w-[50ch]">Automated turn notifications — customers return exactly when needed. No app to install.</p>
					</div>
					<div class="space-y-2">
						<div class="flex items-center gap-2.5">
							<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" class="text-gold-accent shrink-0"><rect x="3" y="4" width="18" height="18" rx="2"/><path d="M16 2v4M8 2v4M3 10h18"/></svg>
							<h3 class="font-satoshi font-semibold text-sm text-primary">Appointment booking</h3>
						</div>
						<p class="text-sm text-muted leading-relaxed max-w-[50ch]">Pick a service and time slot, get a WhatsApp confirmation. No back-and-forth.</p>
					</div>
					<div class="space-y-2">
						<div class="flex items-center gap-2.5">
							<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" class="text-gold-accent shrink-0"><path d="M20 10c0 5.523-8 12-8 12s-8-6.477-8-12a8 8 0 0116 0z"/><path d="M9.5 12l1.8 1.8L14.5 10"/></svg>
							<h3 class="font-satoshi font-semibold text-sm text-primary">Arrival check-in</h3>
						</div>
						<p class="text-sm text-muted leading-relaxed max-w-[50ch]">Tap "on my way," then confirm arrival with the 4-digit PIN posted at the counter. Staff knows exactly when to expect you.</p>
					</div>
				</div>
			</div>
		</section>

		<!-- FAQ -->
		<section class="w-full max-w-3xl mx-auto px-6 pb-20 md:pb-28">
			<h2 id="faq-heading" class="font-satoshi font-bold text-2xl md:text-3xl tracking-[-0.02em] text-center mb-10" style="text-wrap: balance;">
				Common questions
			</h2>
			<div class="space-y-px rounded-xl overflow-hidden bg-white/[0.03]">
				{#each faqs as faq, i}
					<div class="faq-item bg-canvas">
						<button
							class="w-full flex items-center justify-between gap-4 px-6 py-5 text-left hover:bg-white/[0.01] transition-colors"
							onclick={() => openFaq = openFaq === i ? -1 : i}
							aria-expanded={openFaq === i}
							aria-controls="faq-{i}"
						>
							<span class="font-satoshi font-semibold text-sm text-primary">{faq.q}</span>
							<svg width="16" height="16" viewBox="0 0 16 16" fill="none" class="shrink-0 text-muted transition-transform duration-200" class:rotate-45={openFaq === i}>
								<path d="M8 3v10M3 8h10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
							</svg>
						</button>
						<!-- Always rendered in SSR HTML (never {#if}-gated) so the FAQPage JSON-LD
						     text always has a matching visible DOM node — only the CSS display
						     state is click-toggled. -->
						<div id="faq-{i}" class="px-6 pb-5 -mt-1 {openFaq === i ? '' : 'hidden'}" role="region">
							<p class="text-sm text-muted leading-relaxed max-w-[60ch]">{faq.a}</p>
							{#if faq.link}
								<a href={faq.link.href} class="inline-block mt-2 text-sm text-gold-accent hover:text-gold-accent/80 transition-colors">{faq.link.label} &rarr;</a>
							{/if}
						</div>
					</div>
				{/each}
			</div>
		</section>

		<!-- Final CTA -->
		<section class="w-full max-w-5xl mx-auto px-6 pb-20 md:pb-28">
			<div class="relative bg-surface border border-white/[0.05] rounded-2xl p-10 md:p-14 text-center machined-edge overflow-hidden">
				<div class="absolute inset-0 pointer-events-none" aria-hidden="true">
					<div class="absolute top-0 left-1/2 -translate-x-1/2 w-[400px] h-[200px] bg-gold-accent/[0.04] blur-[100px]"></div>
				</div>
				<div class="relative space-y-5">
					<h2 class="font-satoshi font-bold text-2xl md:text-3xl tracking-[-0.02em]" style="text-wrap: balance;">
						Ready to end the waiting room chaos?
					</h2>
					<p class="text-muted max-w-lg mx-auto" style="text-wrap: pretty;">Set up takes under 10 minutes. No hardware, no app downloads for customers. Just a link and WhatsApp.</p>
					<div class="pt-2">
						<a href={ctaUrl} class="inline-block w-full sm:w-auto sm:min-w-64">
							<Button variant="primary">Message us on WhatsApp</Button>
						</a>
					</div>
				</div>
			</div>
		</section>
	</main>

	<SiteFooter />
</div>

<style>
	/* WhatsApp chat bubbles */
	.wa-bubble {
		max-width: 220px;
		padding: 6px 8px 2px;
		border-radius: 8px;
		position: relative;
	}
	.wa-bubble-in {
		background-color: #1F2C33;
		margin-right: auto;
		border-top-left-radius: 2px;
	}
	.wa-bubble-out {
		background-color: #005C4B;
		margin-left: auto;
		border-top-right-radius: 2px;
	}
	.wa-bubble-gold {
		border: 1px solid rgba(200, 169, 107, 0.25);
		box-shadow: 0 0 12px rgba(200, 169, 107, 0.08);
	}
	.wa-time {
		display: block;
		text-align: right;
		font-size: 10px;
		color: #8696A0;
		margin-top: 2px;
		padding-bottom: 2px;
	}

	/* Sequential hero choreography — state-driven, not CSS delay */
	.hero-text {
		opacity: 0;
		transform: translateY(12px);
		transition: opacity 0.5s cubic-bezier(0.16, 1, 0.3, 1), transform 0.5s cubic-bezier(0.16, 1, 0.3, 1);
	}
	.hero-visible {
		opacity: 1;
		transform: translateY(0);
	}

	.phone-entrance {
		opacity: 0;
		transform: translateY(20px);
		transition: opacity 0.6s cubic-bezier(0.16, 1, 0.3, 1), transform 0.6s cubic-bezier(0.16, 1, 0.3, 1);
	}
	.phone-visible {
		opacity: 1;
		transform: translateY(0);
	}

	.bubble-enter {
		opacity: 0;
		transform: translateY(6px) scale(0.97);
		transition: opacity 0.35s cubic-bezier(0.16, 1, 0.3, 1), transform 0.35s cubic-bezier(0.16, 1, 0.3, 1);
	}
	.bubble-visible {
		opacity: 1;
		transform: translateY(0) scale(1);
	}

	/* Gold notification pulse — only after bubble is visible */
	.gold-pulse {
		animation: gold-glow 2.5s ease-in-out 0.4s infinite;
	}

	@keyframes gold-glow {
		0%, 100% { box-shadow: 0 0 12px rgba(200, 169, 107, 0.08); }
		50% { box-shadow: 0 0 20px rgba(200, 169, 107, 0.18); }
	}

	/* Hero glow breathing */
	.hero-glow {
		animation: glow-breathe 6s ease-in-out infinite;
	}

	@keyframes glow-breathe {
		0%, 100% { opacity: 1; transform: translate(-50%, -50%) scale(1); }
		50% { opacity: 0.7; transform: translate(-50%, -50%) scale(1.05); }
	}

	/* Phone mockup hover */
	.wa-phone {
		transition: transform 0.4s cubic-bezier(0.16, 1, 0.3, 1);
	}
	.wa-phone:hover {
		transform: rotate(-1deg) translateY(-4px);
	}

	.step-cell {
		transition: background-color 0.2s ease-out;
	}
	.step-cell:hover {
		background-color: #0A0A0A;
	}
	.step-visual {
		padding-bottom: 4px;
	}

	@media (prefers-reduced-motion: reduce) {
		.hero-text, .phone-entrance, .bubble-enter {
			opacity: 1;
			transform: none;
			transition: none;
		}
		.gold-pulse {
			animation: none;
		}
		.hero-glow {
			animation: none;
		}
		.wa-phone:hover {
			transform: none;
		}
	}
</style>
