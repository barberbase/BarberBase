<script lang="ts">
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import Seo from '$lib/components/Seo.svelte';
	import { BRAND } from '$lib/site-config';

	const guides = [
		{ id: 'how-whatsapp-queue-management-works', title: 'How WhatsApp queue management works' },
		{ id: 'reducing-wait-time-complaints', title: 'Reducing walk-in wait-time complaints without hiring more staff' },
		{ id: 'setting-up-appointment-booking', title: 'Setting up appointment booking for your salon' },
		{ id: 'what-is-an-arrival-pin', title: 'What an arrival PIN is and why it matters' },
		{ id: 'multi-barber-queue-management', title: 'Queue management for multi-barber shops' }
	];
</script>

<Seo
	title="Resources — {BRAND}"
	description="Guides on running a walk-in barbershop queue and appointment booking with {BRAND}."
	path="/resources"
/>

<div class="min-h-screen bg-canvas text-primary font-manrope flex flex-col">
	<SiteHeader activePage="/resources" />

	<main id="main-content" class="flex-grow w-full max-w-3xl mx-auto px-6 py-12 md:py-20">
		<h1 class="font-satoshi font-extrabold text-3xl md:text-4xl tracking-[-0.03em] mb-10" style="text-wrap: balance;">
			Resources
		</h1>

		<nav aria-label="Guides on this page" class="mb-12 space-y-px rounded-xl overflow-hidden bg-white/[0.03]">
			{#each guides as guide}
				<a href="#{guide.id}" class="block bg-canvas px-6 py-4 text-sm font-medium text-primary hover:bg-white/[0.01] transition-colors">
					{guide.title}
				</a>
			{/each}
		</nav>

		<div class="article-prose prose prose-invert max-w-none space-y-16">
			<article id="how-whatsapp-queue-management-works">
				<h2>How WhatsApp queue management works</h2>
				<p>A walk-in barbershop queue usually runs on a physical line: customers wait in the shop, staff call names, and anyone unsure of their position has to ask. That's fine for a quiet afternoon and breaks down the moment the shop gets busy — the line gets long, the room gets crowded, and staff spend as much time answering "how much longer" as they do cutting hair.</p>
				<p>BarberBase replaces the physical line with a queue a customer joins from their own phone — by scanning a QR code or tapping a link, no app or account needed. There's nothing to install and nothing to sign up for; the join happens in whatever browser is already open.</p>
				<p>From that point, the customer sees their live position and estimated wait, and both update automatically as the queue moves. There's no refreshing a page or asking a staff member — the number in front of them is the current state of the line.</p>
				<p>The notification layer runs on WhatsApp, not a separate app. When a customer is next, when their spot is released, or when there's a delay, they get a WhatsApp message — the same channel they already use, already open on their phone, with no new notification permissions to grant and nothing new to check.</p>
				<p>When a customer is heading back to the shop, they tap "on my way" on their status page, then confirm arrival with the 4-digit PIN posted at the shop counter. That two-step confirmation gives staff a real signal that someone is actually returning, rather than a queue entry that might just be someone testing whether they can hold a spot indefinitely.</p>
				<p>Appointments run through the same system. A customer picks a service and a time slot on the shop's page, and gets a WhatsApp confirmation — no separate booking tool, no phone call.</p>
				<p>On the shop side, staff work from one dashboard: call the next customer, start the service, mark it complete, add a walk-in, or assign a specific barber. Whether a customer walked in or booked ahead, they move through the same board. The dashboard and the customer's phone show the same state — nobody is relaying information between a paper list and a screen, and nothing has to be typed twice.</p>
				<p>The result is a queue that behaves the way waiting already works in a customer's head: they know where they stand, they're told when it matters, and they don't have to stand in the shop to find out.</p>
				<p>See it in action: <a href="/demo">try the live demo</a>.</p>
			</article>

			<article id="reducing-wait-time-complaints">
				<h2>Reducing walk-in wait-time complaints without hiring more staff</h2>
				<p>Most wait-time complaints aren't really about the length of the wait — they're about not knowing how long it is. A customer who can see they're third in line and roughly how long that takes tolerates the wait very differently than a customer standing in a room with no information, wondering if they've been forgotten. The wait is often the same length either way; the experience of it isn't.</p>
				<p>The fix isn't more staff on the floor to manage the room — it's giving customers the information they'd otherwise be asking for out loud. With BarberBase, a customer joins the queue from their phone and sees their live position and estimated wait update automatically. That answers "how much longer" before it gets asked, which means one less interruption for whoever's cutting hair.</p>
				<p>Because the queue is virtual, customers don't have to occupy the physical waiting area at all. They can run an errand, wait in their car, or sit next door, and come back when their turn is close — which also means fewer people crowding a small waiting room at once, and less pressure on a shop that doesn't have much seating to begin with.</p>
				<p>WhatsApp handles the rest of the communication automatically: a message when a customer is next, when a spot is released, or when there's a delay. Staff aren't the ones fielding "is it my turn yet" or explaining why the line hasn't moved — the queue state does that on its own, consistently, without depending on whoever happens to be free to answer.</p>
				<p>When a customer does head back to the shop, they tap "on my way," then confirm arrival with the 4-digit PIN posted at the counter once they're there. That gives staff a real signal that someone is actually coming, rather than guessing whether an empty-looking queue entry will ever show up — which matters when deciding whether to call the next customer or hold a spot a little longer.</p>
				<p>The staff dashboard itself is built around the same idea: call the next customer, start the service, mark it complete, add a walk-in, or assign a specific barber, all from one board. Nobody needs to cross-reference a paper list against what a customer's phone is showing them, because both are reading the same live state.</p>
				<p>None of this requires new hires — it requires moving the information customers already want from "ask a staff member" to "check your phone," and letting WhatsApp carry the updates that would otherwise be shouted across a room.</p>
			</article>

			<article id="setting-up-appointment-booking">
				<h2>Setting up appointment booking for your salon</h2>
				<p>Appointment booking on BarberBase works around the same idea as the walk-in queue: a customer picks what they want, gets a slot, and everything after that runs through WhatsApp — no separate booking tool for the shop to run alongside the queue.</p>
				<p>On a shop's page, a customer selects the services they want. Each service has a duration, and that duration is what determines which time slots are actually available — a longer service blocks out a longer slot, so the shop's schedule reflects what a booking will actually take, not a fixed generic appointment length applied to every visit regardless of what's being done.</p>
				<p>Once the customer picks a slot, they get a WhatsApp confirmation. There's no separate account to create, no phone call to place, and no back-and-forth to lock in a time — the confirmation is the booking, sent to a channel the customer is already checking.</p>
				<p>On the shop side, the same staff dashboard used for walk-ins handles appointments too — staff call, start, and complete visits from one board, whether the customer walked in or booked ahead. That's the part that matters most for a shop already running a queue: appointments don't need a second system next to it. They sit in the same dashboard, the same queue state, the same WhatsApp channel, so staff aren't switching between two different tools depending on how a customer showed up.</p>
				<p>For a shop deciding whether to offer appointments alongside walk-ins, the practical question is really about the service catalog — set accurate durations for each service, since that's what booking slots are built from. A haircut and a full grooming package shouldn't book the same length of slot, and getting durations right up front is what keeps the schedule honest later.</p>
				<p>Staff don't need a separate screen for appointments versus walk-ins — the same dashboard that runs the queue is where booked visits get called, started, and completed too, so there's one place to check, not two.</p>
				<p>For customers, the appeal is the same as the queue itself: no account to create, no app to download, and no phone call needed to lock in a time. Picking a service and a slot on the shop's page, and getting a WhatsApp confirmation back, is the whole interaction.</p>
				<p>A shop that already runs walk-ins through BarberBase isn't adopting a new tool to add appointments — it's turning on a second way for customers to reach the same queue and the same dashboard staff are already using.</p>
			</article>

			<article id="what-is-an-arrival-pin">
				<h2>What an arrival PIN is and why it matters</h2>
				<p>A queue that only tracks position has a gap: it doesn't know whether the person at the front of the line is actually about to walk through the door. A customer might join, get comfortable somewhere else, and simply not come back — and staff have no way to tell that apart from someone who's two minutes away and about to walk in.</p>
				<p>BarberBase closes that gap in two steps. First, when a customer is heading back, they tap "on my way" on their queue status page — a deliberate signal of intent, not something the system assumes on its own. Second, once they're actually at the shop, they confirm arrival by entering a 4-digit PIN posted on a card at the counter.</p>
				<p>That second step is what makes the confirmation meaningful: the PIN only exists physically at the shop, so entering it correctly means the customer is genuinely there, not just claiming to be on their way from somewhere else.</p>
				<p>For staff, this means the queue reflects reality more closely: a customer who's confirmed arrival is worth holding a chair for, and a queue entry that's gone quiet without either signal is a much safer one to release. That's also what backs the "spot released" WhatsApp update — a shop can free up a slot for the next customer instead of holding it indefinitely against an entry that may never arrive, which keeps the line moving for everyone behind it.</p>
				<p>For customers, both steps are quick — tapping a button, then typing four digits off a card at the counter. It's the last piece of the same flow that started with joining the queue: join, track your position, get notified, say you're on your way, confirm you've arrived.</p>
				<p>It also protects the customer's own spot. Without any arrival signal, a shop calling a customer who genuinely is on their way has no way to distinguish that from someone who's already given up and left. The PIN is what tells the shop "I'm actually here" — rather than leaving that judgment to guesswork on either side.</p>
				<p>Because the PIN card lives at the counter rather than inside an app or a message, there's nothing for a customer to lose track of between joining the queue and showing up — it's simply there when they arrive, the same way it is for the customer ahead of them and the one behind.</p>
				<p>For more on how this confirmation flow affects no-shows in practice, see <a href="/blog/why-arrival-confirmation-cuts-no-shows">why arrival confirmation cuts no-shows</a> and <a href="/blog/why-the-arrival-pin-is-at-the-counter">why the arrival PIN lives at the counter</a>.</p>
			</article>

			<article id="multi-barber-queue-management">
				<h2>Queue management for multi-barber shops</h2>
				<p>A shop with more than one barber has a coordination problem a single-chair shop doesn't: who's next isn't just about queue position, it's about which barber is free, and whether a customer has a preference.</p>
				<p>BarberBase's staff dashboard supports barber selection directly — staff can assign a customer to a specific barber when calling them next, alongside the usual call next, start, complete, and walk-in actions. That means the dashboard reflects each barber's own queue state, not just one combined line, while still giving staff a single board to run the whole shop from.</p>
				<p>For customers, the flow doesn't change — they join the queue the same way, from a link or QR code, and see their live position and estimated wait. Barber assignment is something staff manage on their side, so it doesn't add a step to how a customer joins or tracks their turn.</p>
				<p>The same WhatsApp updates apply regardless of which barber a customer ends up with: a message when they're next, if their spot is released, or if there's a delay. And the same "on my way" tap and counter-PIN confirmation apply at the end of the flow, whichever chair a customer is heading for.</p>
				<p>For a shop with multiple barbers, the practical benefit is that the queue stops being a single undifferentiated line — it becomes something staff can actually route, without customers having to know or care how that routing works.</p>
				<p>This matters most at the moment a barber finishes a service and staff need to decide who's next for that specific chair. Rather than working from a single shared list and figuring out who's compatible with which barber, staff assign directly from the dashboard — the same board used for calling walk-ins and appointments, just with barber selection built into that action.</p>
				<p>It also means a customer's preference for a specific barber doesn't have to be tracked separately from the queue itself, or relayed verbally between staff. It's part of the same call-next action, on the same board, alongside starting and completing the visit.</p>
				<p>None of this changes what a customer experiences when joining — the queue-join flow, the live position, and the WhatsApp updates are identical whether a shop has one chair or five. The routing is entirely a staff-side concern, which is where it belongs: a customer shouldn't have to understand a shop's staffing to know where they stand in line.</p>
				<p>Related reading: <a href="/blog/mixed-walk-in-and-remote-queues">running walk-ins and remote joins as one queue</a> — the same single-board approach, seen from the queue side rather than the barber side.</p>
			</article>
		</div>
	</main>

	<SiteFooter />
</div>

<style>
	.article-prose {
		--tw-prose-body: var(--color-muted);
		--tw-prose-headings: var(--color-primary);
		--tw-prose-bold: var(--color-primary);
		--tw-prose-links: var(--color-gold-accent);
		--tw-prose-bullets: var(--color-dim);
		--tw-prose-hr: color-mix(in srgb, white 3%, transparent);
		--tw-prose-quotes: var(--color-primary);
		--tw-prose-quote-borders: var(--color-gold-accent);
	}
</style>
