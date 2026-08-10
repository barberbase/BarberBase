<script lang="ts">
	import { onMount } from 'svelte';
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import Seo from '$lib/components/Seo.svelte';
	import {
		BRAND,
		SALES_WHATSAPP,
		CONTACT_EMAIL,
		LEGAL_ENTITY_NAME,
		UDYAM_NUMBER,
		SITE_URL,
		ORGANIZATION_JSON_LD
	} from '$lib/site-config';

	const LIVE_SHOP_URL = 'https://barberbase.in/star-salon/bhayander';
	const demoUrl = SALES_WHATSAPP
		? `https://wa.me/${SALES_WHATSAPP}?text=Hi,%20I'd%20like%20a%20BarberBase%20demo%20for%20my%20shop.`
		: `mailto:${CONTACT_EMAIL}?subject=BarberBase%20demo%20for%20my%20shop`;

	// --- Hero queue strip -------------------------------------------------
	// People move, positions stay: the row list translates, the slot column
	// (waits + the gold "in chair" band) is a static overlay. One animated
	// element for the whole signature.
	const QUEUE = [
		{ name: 'Imran', service: 'Beard trim' },
		{ name: 'Sagar', service: 'Fade' },
		{ name: 'Devendra', service: 'Cut and beard' },
		{ name: 'Faisal', service: 'Kids cut' },
		{ name: 'Nitin', service: 'Fade' },
		{ name: 'Ashfaq', service: 'Shave' },
		{ name: 'Prashant', service: 'Cut and beard' },
		{ name: 'Salim', service: 'Fade' },
		{ name: 'Vikrant', service: 'Beard trim' },
		{ name: 'Junaid', service: 'Kids cut' }
	];
	// Slot 1 is the chair, so it carries a label instead of an estimate.
	const SLOT_WAITS = [null, '~10 min', '~25 min', '~35 min', '~45 min'];

	// 6 rows for 5 visible slots: the sixth is staged under the clip so the
	// arriving row is never a pop-in.
	let rows = $state(QUEUE.slice(0, 6).map((r, i) => ({ ...r, token: 18 + i })));
	let arrival = 6;
	let sliding = $state(false);
	let list: HTMLElement | undefined = $state();

	function advance() {
		if (sliding || !list) return;
		list.style.willChange = 'transform';
		sliding = true;
	}

	function onSlideEnd() {
		if (!sliding) return;
		sliding = false;
		if (list) list.style.willChange = '';
		const next = QUEUE[arrival % QUEUE.length];
		rows = [...rows.slice(1), { ...next, token: rows[rows.length - 1].token + 1 }];
		arrival += 1;
	}

	onMount(() => {
		// No JS and no reduced motion both land on the same place: the fully
		// rendered final state. Only the "from" state is added by script.
		if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;

		document.documentElement.classList.add('anim');

		// Reveal is driven by a sweep rather than by each entry, because two cases
		// produce no usable entry at all: a section taller than the viewport never
		// reaches the threshold, and a viewport that jumps clean over a section
		// (restored scroll position, anchor link) never changes its ratio. Either
		// one would strand content at opacity 0. The sweep reads layout only while
		// sections are still pending, then tears itself down for good.
		const pending = new Set<Element>(document.querySelectorAll('[data-reveal]'));
		let io: IntersectionObserver | undefined;

		const sweep = () => {
			for (const el of pending) {
				if (el.getBoundingClientRect().top >= window.innerHeight) continue;
				el.classList.add('in');
				pending.delete(el);
			}
			if (!pending.size) stopReveal();
		};
		// scrollend fires once per gesture, not per frame, so it costs nothing on
		// scroll. It is the backstop for jumps; where it is unsupported the
		// observer and the initial sweep still cover every ordinary scroll.
		const stopReveal = () => {
			io?.disconnect();
			io = undefined;
			window.removeEventListener('scrollend', sweep);
		};

		io = new IntersectionObserver(sweep, { threshold: 0, rootMargin: '0px 0px -4% 0px' });
		for (const el of pending) io.observe(el);
		window.addEventListener('scrollend', sweep, { passive: true });
		requestAnimationFrame(sweep);

		let timer = 0;
		const start = () => {
			if (!timer) timer = window.setInterval(advance, 4500);
		};
		const stop = () => {
			clearInterval(timer);
			timer = 0;
		};
		const onVisibility = () => (document.hidden ? stop() : start());

		start();
		document.addEventListener('visibilitychange', onVisibility);

		return () => {
			stop();
			document.removeEventListener('visibilitychange', onVisibility);
			stopReveal();
			document.documentElement.classList.remove('anim');
		};
	});

	// --- Content ----------------------------------------------------------
	const shopSide = [
		{
			title: '4-digit PIN',
			body: 'The customer confirms he has arrived by entering the PIN at your counter. No arguments about who came first.'
		},
		{
			title: 'Daily record',
			body: 'Every service and what it earned, written down as it happens. Close the day and it is already done.'
		},
		{
			title: 'Weekly summary',
			body: 'A summary of your week arrives on WhatsApp. You do not have to open anything to get it.'
		},
		{
			title: 'Button at the chair',
			body: 'Optional. A physical Next Client button by the mirror, if you would rather not touch a screen mid-cut.'
		}
	];

	const thread = [
		{
			out: false,
			gold: false,
			text: "You're #11 at Star Salon, Bhayander. About 55 min.",
			time: '10:12 AM'
		},
		{ out: false, gold: false, text: "You're #3 now. About 12 min.", time: '10:47 AM' },
		{ out: false, gold: true, text: "You're next. Head over.", time: '10:56 AM' },
		{ out: true, gold: false, text: 'Coming in 5 min', time: '10:56 AM' }
	];

	const faqs: { q: string; a: string; link?: { href: string; label: string } }[] = [
		{
			q: 'Is BarberBase a real company? Who runs it?',
			a: `Yes. BarberBase is built and operated by ${LEGAL_ENTITY_NAME} (Udyam ${UDYAM_NUMBER}).`,
			link: { href: '/about', label: 'See our full registered-entity details and contact info' }
		},
		{
			q: 'Do my customers need to download an app?',
			a: 'No. They join the queue from WhatsApp. Tapping your link or scanning the code at your counter is the whole thing. No account, no password.'
		},
		{
			q: 'How does a customer see the queue before he comes in?',
			a: 'He sees his position and how long the wait is on his phone, updating as the queue moves. He leaves home when his turn is two or three away.'
		},
		{
			q: 'What about the people who just walk in?',
			a: 'Walk-ins and customers who joined from home sit in the same list, in the order they arrived. Your staff sees one queue, not two.'
		},
		{
			q: 'How does the shop know a customer has actually arrived?',
			a: 'He enters the 4-digit PIN posted at your counter. That confirms he is in the shop, so nobody holds a chair for someone still at home.'
		},
		{
			q: 'What do I get at the end of the day and the week?',
			a: "A record of the day's services and what they earned, written as they happen. A summary of the week arrives on your WhatsApp."
		}
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

{#snippet cta(href: string, label: string, primary: boolean, external: boolean)}
	<a
		{href}
		target={external ? '_blank' : null}
		rel={external ? 'noopener noreferrer' : null}
		class="inline-flex w-full items-center justify-center rounded-full border px-6 py-3.5 text-center text-xs font-body uppercase tracking-widestUI transition-transform duration-150 select-none active:scale-[0.98] sm:w-auto sm:min-w-56 {primary
			? 'border-transparent bg-primary font-bold text-canvas hover:bg-[#D5D2C9]'
			: 'border-white/10 bg-transparent font-semibold text-primary hover:bg-white/[0.02]'}"
	>
		{label}
	</a>
{/snippet}

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

<div class="flex min-h-screen flex-col bg-canvas font-body text-primary">
	<SiteHeader />

	<main id="main-content" class="flex-grow">
		<!-- 1. Hero -->
		<section class="mx-auto w-full max-w-6xl px-6 pt-12 pb-16 md:pt-20 md:pb-28">
			<div class="grid grid-cols-1 items-center gap-10 md:grid-cols-9 md:gap-12">
				<div class="md:col-span-5">
					<h1
						class="font-display text-[2.5rem] leading-[1.02] font-extrabold tracking-tightest sm:text-[3rem] md:text-[4rem]"
					>
						Never miss a customer.
					</h1>
					<p class="mt-5 max-w-[34ch] text-lg leading-relaxed text-muted md:text-xl">
						Sunday rush. Ten men waiting. The eleventh looks in, turns around, and walks out.
					</p>
					<div class="mt-8 flex flex-col gap-3 sm:flex-row">
						{@render cta(LIVE_SHOP_URL, 'See a live shop', true, true)}
						{@render cta(demoUrl, 'Book a demo on WhatsApp', false, false)}
					</div>
				</div>

				<div class="md:col-span-4">
					<p class="sr-only">
						A live queue at a barbershop. Each customer shows a token number, a name and a service,
						with the estimated wait for every position behind the chair.
					</p>
					<div class="strip machined-edge" aria-hidden="true">
						<div class="flex h-10 items-center justify-between border-b border-white/[0.05] px-4">
							<span class="text-[11px] text-muted">Star Salon, Bhayander</span>
							<span class="font-mono text-[10px] tracking-widestUI text-muted">QUEUE</span>
						</div>

						<div class="strip-clip">
							<ul class="strip-list" class:sliding bind:this={list} ontransitionend={onSlideEnd}>
								{#each rows as row (row.token)}
									<li class="strip-row">
										<span class="font-mono text-[13px] text-muted tabular-nums">{row.token}</span>
										<span class="min-w-0">
											<span class="block truncate text-sm font-medium text-primary">{row.name}</span
											>
											<span class="block truncate text-[11px] text-muted">{row.service}</span>
										</span>
									</li>
								{/each}
							</ul>

							<div class="strip-slots">
								{#each SLOT_WAITS as wait, i}
									<div class="strip-slot" class:chair={i === 0}>
										{#if wait === null}
											<span class="font-mono text-[10px] tracking-widestUI text-gold-accent"
												>IN CHAIR</span
											>
										{:else}
											<span class="font-mono text-[11px] text-muted tabular-nums">{wait}</span>
										{/if}
									</div>
								{/each}
							</div>
						</div>
					</div>
				</div>
			</div>
		</section>

		<div class="pole" aria-hidden="true"></div>

		<!-- 2. The loss -->
		<section class="w-full bg-matte py-16 md:py-28">
			<div class="mx-auto grid w-full max-w-6xl grid-cols-1 gap-8 px-6 md:grid-cols-12">
				<h2
					data-reveal
					class="font-display text-[1.75rem] leading-[1.12] font-bold tracking-tightest md:col-span-12 md:text-[2.75rem]"
				>
					The customer you lost on Sunday has no name.
				</h2>
				<div
					data-reveal
					class="space-y-5 text-lg leading-relaxed text-muted md:col-span-7 md:col-start-5 md:text-xl"
				>
					<p class="max-w-[46ch]">
						He stood at the door for four seconds. Counted the heads, counted the minutes, and went
						to the shop down the road.
					</p>
					<p class="max-w-[46ch]">
						You never got his name. You never got his number. Nothing in your day says he was ever
						there.
					</p>
				</div>
			</div>
		</section>

		<!-- 3. The fix -->
		<section class="mx-auto w-full max-w-6xl px-6 py-16 md:py-28">
			<div class="grid grid-cols-1 items-start gap-12 md:grid-cols-12 md:gap-16">
				<div class="md:col-span-6">
					<h2
						data-reveal
						class="font-display text-[1.75rem] leading-[1.12] font-bold tracking-tightest md:text-[2.5rem]"
					>
						He can count the heads from home.
					</h2>
					<p data-reveal class="mt-5 max-w-[48ch] leading-relaxed text-muted md:text-lg">
						Same customer, same Sunday. This time he opens WhatsApp, sees he is eleventh, and gets
						on with his morning. When his turn is two or three away, he leaves the house. He walks
						in, sits down, and you cut.
					</p>

					<ul data-reveal class="mt-10">
						<li class="py-4 text-primary">Joins from WhatsApp. No app to download.</li>
						<li class="border-t border-white/[0.05] py-4 text-primary">
							Sees his position and the wait before he leaves home.
						</li>
						<li class="border-t border-white/[0.05] py-4 text-primary">
							Walks in when his turn is two or three away.
						</li>
					</ul>
				</div>

				<div class="md:col-span-6 md:justify-self-end" data-reveal>
					<div
						class="wa mx-auto w-full max-w-[300px] overflow-hidden rounded-xl border border-white/[0.06]"
					>
						<div
							class="flex items-center gap-2.5 border-b border-white/[0.04] bg-[#1F2C33] px-4 py-3"
						>
							<span
								class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[#25D366]/20"
							>
								<svg width="15" height="15" viewBox="0 0 24 24" fill="#25D366" aria-hidden="true"
									><path
										d="M12 0C5.373 0 0 5.373 0 12c0 2.625.846 5.059 2.284 7.034L.789 23.492a.5.5 0 00.611.611l4.458-1.495A11.943 11.943 0 0012 24c6.627 0 12-5.373 12-12S18.627 0 12 0zm0 22c-2.29 0-4.403-.764-6.1-2.052l-.426-.33-2.822.946.946-2.822-.33-.426A9.935 9.935 0 012 12C2 6.477 6.477 2 12 2s10 4.477 10 10-4.477 10-10 10z"
									/></svg
								>
							</span>
							<span>
								<span class="block text-[13px] font-semibold text-[#E9EDEF]">BarberBase</span>
								<span class="block text-[10px] text-[#A8B8C0]">Business account</span>
							</span>
						</div>

						<div class="space-y-2.5 bg-[#0B141A] px-3 py-4">
							{#each thread as m, i}
								<div
									class="wa-bubble {m.out ? 'wa-out' : 'wa-in'}"
									class:wa-gold={m.gold}
									style="--i:{i}"
								>
									<p
										class="text-[12.5px] leading-[1.45] text-[#E9EDEF]"
										class:font-semibold={m.gold}
									>
										{m.text}
									</p>
									<span class="wa-time">{m.time}</span>
								</div>
							{/each}
						</div>
					</div>
				</div>
			</div>
		</section>

		<!-- 4. What the shop side looks like -->
		<section class="mx-auto w-full max-w-6xl px-6 pb-16 md:pb-28">
			<h2
				data-reveal
				class="font-display text-[1.75rem] leading-[1.12] font-bold tracking-tightest md:text-[2.5rem]"
			>
				One list. One tap.
			</h2>
			<p data-reveal class="mt-5 max-w-[52ch] leading-relaxed text-muted md:text-lg">
				Walk-ins and the ones who joined from home sit in the same queue, in the order they arrived.
				Tap Call Next. The chair never sits empty waiting for someone to come back.
			</p>

			<div class="mt-10 grid grid-cols-1 gap-3 md:grid-cols-2">
				<div
					data-reveal
					class="machined-edge rounded-xl border border-white/[0.05] bg-surface p-7 md:col-span-2 md:p-9"
				>
					<h3 class="font-display text-xl font-bold tracking-tightest md:text-2xl">Call Next</h3>
					<span class="mt-3 block h-px w-16 bg-gold-accent"></span>
					<p class="mt-4 max-w-[46ch] leading-relaxed text-muted">
						One tap moves the chair forward. Your staff never has to work out whose turn it is.
					</p>
				</div>

				{#each shopSide as cell}
					<div
						data-reveal
						class="machined-edge rounded-xl border border-white/[0.03] bg-matte p-7 md:p-8"
					>
						<h3 class="font-display text-base font-bold text-primary">{cell.title}</h3>
						<p class="mt-3 text-sm leading-relaxed text-muted">{cell.body}</p>
					</div>
				{/each}
			</div>
		</section>

		<div class="pole" aria-hidden="true"></div>

		<!-- 5. Why no app -->
		<section class="mx-auto w-full max-w-6xl px-6 py-16 md:py-28">
			<h2
				data-reveal
				class="max-w-[16ch] font-display text-[2rem] leading-[1.06] font-extrabold tracking-tightest md:text-[3.25rem]"
			>
				Your customer will not download an app. He doesn't have to.
			</h2>
			<p data-reveal class="mt-7 max-w-[52ch] text-lg leading-relaxed text-muted md:text-xl">
				WhatsApp is already open on his phone. That is the whole installation. He taps your link, or
				scans the code at your counter, and he is in the queue. No sign-up, no password, no download
				eating his data.
			</p>
		</section>

		<!-- 6. Live demo -->
		<section class="mx-auto w-full max-w-6xl px-6 pb-16 md:pb-28">
			<div
				data-reveal
				class="machined-edge rounded-xl border border-white/[0.05] bg-surface p-8 md:p-12"
			>
				<h2
					class="font-display text-[1.75rem] leading-[1.12] font-bold tracking-tightest md:text-[2.25rem]"
				>
					See it running in a real shop.
				</h2>
				<p class="mt-4 max-w-[48ch] leading-relaxed text-muted md:text-lg">
					Star Salon in Bhayander is live right now. Open the page, look at the queue, join it if
					you like.
				</p>
				<div class="mt-8 flex flex-col gap-3 sm:flex-row">
					{@render cta(LIVE_SHOP_URL, 'See a live shop', true, true)}
					{@render cta(demoUrl, 'Book a demo on WhatsApp', false, false)}
				</div>
			</div>
		</section>

		<!-- 7. FAQ -->
		<section class="mx-auto w-full max-w-6xl px-6 pb-20 md:pb-28">
			<div class="max-w-3xl">
				<h2
					id="faq-heading"
					class="font-display text-[1.75rem] leading-[1.12] font-bold tracking-tightest md:text-[2.25rem]"
				>
					Common questions
				</h2>
				<div class="mt-8 border-t border-white/[0.05]">
					{#each faqs as faq, i}
						<div class="faq-item border-b border-white/[0.05]">
							<button
								class="flex w-full items-center justify-between gap-4 py-5 text-left transition-colors hover:text-primary"
								onclick={() => (openFaq = openFaq === i ? -1 : i)}
								aria-expanded={openFaq === i}
								aria-controls="faq-{i}"
							>
								<span class="font-display text-[15px] font-bold text-primary">{faq.q}</span>
								<svg
									width="16"
									height="16"
									viewBox="0 0 16 16"
									fill="none"
									aria-hidden="true"
									class="shrink-0 text-muted transition-transform duration-200"
									class:rotate-45={openFaq === i}
								>
									<path
										d="M8 3v10M3 8h10"
										stroke="currentColor"
										stroke-width="1.5"
										stroke-linecap="round"
									/>
								</svg>
							</button>
							<!-- Always in the SSR HTML, never {#if}-gated, so every FAQPage JSON-LD
						     answer has a matching visible DOM node. Only display state toggles. -->
							<div id="faq-{i}" class="pb-5 {openFaq === i ? '' : 'hidden'}" role="region">
								<p class="max-w-[62ch] text-sm leading-relaxed text-muted">{faq.a}</p>
								{#if faq.link}
									<a
										href={faq.link.href}
										class="mt-2 inline-block text-sm text-gold-accent underline underline-offset-4 hover:text-gold-accent/80"
										>{faq.link.label}</a
									>
								{/if}
							</div>
						</div>
					{/each}
				</div>
			</div>
		</section>
	</main>

	<SiteFooter />
</div>

<style>
	/* DESIGN.md sets display type at -0.045em tracking, which is right for the
	   letterforms but closes the word gaps at hero sizes ("Thecustomeryoulost").
	   Buying the spaces back here keeps the token intact. */
	h1,
	h2 {
		word-spacing: 0.09em;
	}

	/* --- Hero queue strip ------------------------------------------------
	   Fixed geometry: the clip is exactly 5 rows tall before, during and
	   after every advance, so the strip can never contribute layout shift. */
	.strip {
		--row-h: 60px;
		border-radius: 12px;
		border: 1px solid rgba(255, 255, 255, 0.05);
		background-color: #141414;
		overflow: hidden;
	}
	.strip-clip {
		position: relative;
		height: calc(var(--row-h) * 5);
		overflow: hidden;
	}
	.strip-list {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.strip-list.sliding {
		transform: translateY(calc(var(--row-h) * -1));
		transition: transform 450ms cubic-bezier(0.16, 1, 0.3, 1);
	}
	.strip-row {
		display: flex;
		align-items: center;
		gap: 14px;
		height: var(--row-h);
		padding: 0 96px 0 16px; /* right gutter reserved for the static slot column */
	}

	/* Slot column: positions do not move, people do. */
	.strip-slots {
		position: absolute;
		inset: 0;
		pointer-events: none;
	}
	.strip-slot {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		height: var(--row-h);
		padding-right: 16px;
	}
	.strip-slot.chair {
		border-left: 1px solid #c8a96b;
		border-bottom: 1px solid rgba(200, 169, 107, 0.2);
	}

	/* --- Barber-pole rule ------------------------------------------------
	   Striped child translated by exactly one horizontal period, so the
	   loop is seamless. Transform only, no background-position. */
	.pole {
		height: 3px;
		overflow: hidden;
	}
	.pole::before {
		content: '';
		display: block;
		height: 3px;
		width: 200%;
		background: repeating-linear-gradient(45deg, #5a5854 0 5px, #1c1c1c 5px 10px);
		animation: pole 2.4s linear infinite;
	}
	@keyframes pole {
		to {
			transform: translateX(-14.1421px);
		}
	}

	/* --- WhatsApp thread -------------------------------------------------- */
	.wa {
		background-color: #0b141a;
	}
	.wa-bubble {
		max-width: 232px;
		padding: 6px 9px 3px;
		border-radius: 8px;
	}
	.wa-in {
		background-color: #1f2c33;
		margin-right: auto;
		border-top-left-radius: 2px;
	}
	.wa-out {
		background-color: #005c4b;
		margin-left: auto;
		border-top-right-radius: 2px;
	}
	.wa-gold {
		border: 1px solid rgba(200, 169, 107, 0.3);
	}
	.wa-time {
		display: block;
		text-align: right;
		font-size: 10px;
		/* #8696A0 (WhatsApp's own) fails AA on #1F2C33; lifted to hold 4.5:1 */
		color: #a8b8c0;
		margin-top: 2px;
	}

	/* --- Section entrances -----------------------------------------------
	   The "from" state is armed by script only, so a no-JS or reduced-motion
	   render is the finished page rather than a blank one. */
	:global(html.anim) [data-reveal] {
		opacity: 0;
		transform: translateY(12px);
	}
	/* .in is added at runtime, so it must be :global() or the compiler prunes
	   these rules and every revealed section ships stuck at opacity 0. */
	:global(html.anim) [data-reveal]:global(.in) {
		opacity: 1;
		transform: none;
		transition:
			opacity 500ms cubic-bezier(0.16, 1, 0.3, 1),
			transform 500ms cubic-bezier(0.16, 1, 0.3, 1);
	}
	:global(html.anim) [data-reveal]:global(.in) .wa-bubble {
		animation: bubble-in 380ms cubic-bezier(0.16, 1, 0.3, 1) both;
		animation-delay: calc(220ms + var(--i) * 140ms);
	}
	@keyframes bubble-in {
		from {
			opacity: 0;
			transform: translateY(8px);
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.strip-list.sliding {
			transform: none;
			transition: none;
		}
		.pole::before {
			animation: none;
		}
	}
</style>
