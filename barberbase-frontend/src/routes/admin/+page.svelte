<script lang="ts">
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	let wizardStep = $state(1);
	let wizardDone = $state(false);

	// Wizard sub-form state
	// Step 1: Service create
	let svcCategoryName = $state('');
	let svcCategoryGender = $state<'men' | 'women' | 'unisex'>('unisex');
	let svcGroupName = $state('');
	let svcVariantName = $state('');
	let svcDuration = $state('');
	let svcPrice = $state('');
	let svcAllowWalkIn = $state(true);
	let svcAllowAppointment = $state(true);
	let svcRequiresAppointment = $state(false);
	let svcIsPopular = $state(false);
	let svcError = $state('');
	let svcSubmitting = $state(false);

	// Step 2: Staff create
	let staffName = $state('');
	let staffPhone = $state('');
	let staffRole = $state<'manager' | 'barber'>('barber');
	let staffError = $state('');
	let staffSubmitting = $state(false);

	// Step 3: WhatsApp
	let waJson = $state('');
	let waError = $state('');
	let waSubmitting = $state(false);
	let waWebhookUrl = $state('');
	let waCopied = $state(false);

	const REQUIRED_BHEJNA_FIELDS = [
		'bhejna_config_version',
		'phone_number',
		'api_key',
		'webhook_secret',
		'whatsapp_status'
	];

	function formatPhone(raw: string): string {
		const cleaned = raw.trim();
		if (cleaned.startsWith('+')) return cleaned;
		if (cleaned.startsWith('91') && cleaned.length === 12) return '+' + cleaned;
		if (cleaned.length === 10) return '+91' + cleaned;
		return cleaned;
	}

	async function submitService() {
		svcError = '';
		const priceNum = parseFloat(svcPrice);
		if (!Number.isInteger(priceNum) || priceNum < 0) {
			svcError = 'Price must be a non-negative whole number (₹)';
			return;
		}
		if (!svcCategoryName || !svcGroupName || !svcVariantName || !svcDuration) {
			svcError = 'All fields are required';
			return;
		}
		svcSubmitting = true;
		try {
			const res = await fetch(`/admin/services?/createVariant`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
				body: new URLSearchParams({
					category_name: svcCategoryName,
					category_gender: svcCategoryGender,
					group_name: svcGroupName,
					variant_name: svcVariantName,
					duration_minutes: svcDuration,
					price_rupees: svcPrice,
					allow_walk_in: String(svcAllowWalkIn),
					allow_appointment: String(svcAllowAppointment),
					requires_appointment: String(svcRequiresAppointment),
					is_popular: String(svcIsPopular)
				})
			});
			if (res.ok || res.status === 201 || res.redirected) {
				wizardStep = 2;
			} else {
				const body = await res.json().catch(() => ({}));
				svcError = body?.message || 'Failed to create service';
			}
		} catch {
			svcError = 'Network error';
		} finally {
			svcSubmitting = false;
		}
	}

	async function submitStaff() {
		staffError = '';
		if (!staffName || !staffPhone) {
			staffError = 'Name and phone are required';
			return;
		}
		staffSubmitting = true;
		try {
			const res = await fetch(`/admin/staff?/addMember`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
				body: new URLSearchParams({
					name: staffName,
					phone_number: formatPhone(staffPhone),
					role: staffRole
				})
			});
			if (res.ok || res.status === 201 || res.redirected) {
				wizardStep = 3;
			} else {
				const body = await res.json().catch(() => ({}));
				staffError = body?.message || 'Failed to add staff';
			}
		} catch {
			staffError = 'Network error';
		} finally {
			staffSubmitting = false;
		}
	}

	async function submitWhatsApp() {
		waError = '';
		let parsed: any;
		try {
			parsed = JSON.parse(waJson);
		} catch {
			waError = 'Invalid JSON — please paste the full config blob';
			return;
		}
		const missing = REQUIRED_BHEJNA_FIELDS.filter((f) => !(f in parsed));
		if (missing.length > 0) {
			waError = `Missing required fields: ${missing.join(', ')}`;
			return;
		}
		waSubmitting = true;
		try {
			const res = await fetch(`/admin/whatsapp?/connect`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
				body: new URLSearchParams({ config_json: waJson })
			});
			const body = await res.json().catch(() => ({}));
			if (res.ok) {
				waWebhookUrl = body?.webhook_url || body?.data?.webhook_url || '';
				wizardDone = true;
			} else {
				waError = body?.message || 'Failed to connect WhatsApp';
			}
		} catch {
			waError = 'Network error';
		} finally {
			waSubmitting = false;
		}
	}

	function copyWebhook() {
		navigator.clipboard.writeText(waWebhookUrl);
		waCopied = true;
		setTimeout(() => (waCopied = false), 2000);
	}
</script>

<svelte:head>
	<title>Admin Panel — BarberBase</title>
	<meta
		name="description"
		content="Owner and manager admin panel: services, staff, shop status, WhatsApp, and analytics"
	/>
</svelte:head>

{#if data.isFirstTime && !wizardDone}
	<!-- ===== ONBOARDING WIZARD ===== -->
	<div class="min-h-screen bg-canvas flex items-center justify-center p-4">
		<div class="w-full max-w-md">
			<!-- Progress dots -->
			<div class="mb-8 flex items-center justify-center gap-2">
				{#each [1, 2, 3] as s}
					<div
						class="h-2 rounded-full transition-all duration-200 {wizardStep === s
							? 'w-8 bg-gold-accent'
							: wizardStep > s
								? 'w-2 bg-gold-accent/60'
								: 'w-2 bg-dim/40'}"
					></div>
				{/each}
			</div>

			<div class="bg-matte border border-white/[0.05] rounded-2xl p-8 machined-edge">
				{#if wizardStep === 1}
					<h1 class="text-xl font-extrabold text-primary font-satoshi tracking-tightest mb-1">Add your first service</h1>
					<p class="text-muted text-sm mb-6">What's the main thing customers come in for?</p>
					{#if svcError}
						<div class="text-sm mb-4 bg-red-950/30 border border-system-error/20 rounded-xl p-3 text-system-error/80">{svcError}</div>
					{/if}
					<div class="space-y-4">
						<div class="grid grid-cols-2 gap-3">
							<div>
								<label for="wz-cat-name" class="block text-xs font-medium text-muted mb-1.5">Category</label>
								<input
									id="wz-cat-name"
									bind:value={svcCategoryName}
									placeholder="Hair"
									class="w-full bg-titanium border border-white/[0.05] rounded-xl px-3 py-2.5 text-primary text-sm focus:outline-none focus:border-gold-accent transition-colors placeholder:text-dim"
								/>
							</div>
							<div>
								<label for="wz-cat-gender" class="block text-xs font-medium text-muted mb-1.5">Gender</label>
								<select
									id="wz-cat-gender"
									bind:value={svcCategoryGender}
									class="w-full bg-titanium border border-white/[0.05] rounded-xl px-3 py-2.5 text-primary text-sm focus:outline-none focus:border-gold-accent transition-colors"
								>
									<option value="men">Men</option>
									<option value="women">Women</option>
									<option value="unisex">Unisex</option>
								</select>
							</div>
						</div>
						<div>
							<label for="wz-group" class="block text-xs font-medium text-muted mb-1.5">Group</label>
							<input
								id="wz-group"
								bind:value={svcGroupName}
								placeholder="Fade"
								class="w-full bg-titanium border border-white/[0.05] rounded-xl px-3 py-2.5 text-primary text-sm focus:outline-none focus:border-gold-accent transition-colors placeholder:text-dim"
							/>
						</div>
						<div>
							<label for="wz-variant" class="block text-xs font-medium text-muted mb-1.5">Variant name</label>
							<input
								id="wz-variant"
								bind:value={svcVariantName}
								placeholder="Mid Fade"
								class="w-full bg-titanium border border-white/[0.05] rounded-xl px-3 py-2.5 text-primary text-sm focus:outline-none focus:border-gold-accent transition-colors placeholder:text-dim"
							/>
						</div>
						<div class="grid grid-cols-2 gap-3">
							<div>
								<label for="wz-duration" class="block text-xs font-medium text-muted mb-1.5">Duration (min)</label>
								<input
									id="wz-duration"
									type="number"
									min="1"
									bind:value={svcDuration}
									placeholder="30"
									class="w-full bg-titanium border border-white/[0.05] rounded-xl px-3 py-2.5 text-primary text-sm focus:outline-none focus:border-gold-accent transition-colors placeholder:text-dim"
								/>
							</div>
							<div>
								<label for="wz-price" class="block text-xs font-medium text-muted mb-1.5">Price (₹)</label>
								<input
									id="wz-price"
									type="number"
									min="0"
									step="1"
									bind:value={svcPrice}
									placeholder="150"
									class="w-full bg-titanium border border-white/[0.05] rounded-xl px-3 py-2.5 text-primary text-sm focus:outline-none focus:border-gold-accent transition-colors placeholder:text-dim"
								/>
							</div>
						</div>
					</div>
					<div class="flex items-center justify-between mt-8">
						<button
							onclick={() => (wizardStep = 2)}
							class="text-muted hover:text-primary transition-colors text-sm">Skip</button
						>
						<button
							onclick={submitService}
							disabled={svcSubmitting}
							id="wz-add-service-btn"
							class="bg-gold-accent hover:bg-gold-accent/90 disabled:opacity-40 text-canvas font-bold py-3 px-8 rounded-full transition-all duration-150 active:scale-[0.98]"
						>
							{svcSubmitting ? 'Saving…' : 'Continue'}
						</button>
					</div>
				{:else if wizardStep === 2}
					<h2 class="text-xl font-extrabold text-primary font-satoshi tracking-tightest mb-1">Add a team member</h2>
					<p class="text-muted text-sm mb-6">Who's working today?</p>
					{#if staffError}
						<div class="text-sm mb-4 bg-red-950/30 border border-system-error/20 rounded-xl p-3 text-system-error/80">{staffError}</div>
					{/if}
					<div class="space-y-4">
						<div>
							<label for="wz-staff-name" class="block text-xs font-medium text-muted mb-1.5">Full name</label>
							<input
								id="wz-staff-name"
								bind:value={staffName}
								placeholder="Ravi Kumar"
								class="w-full bg-titanium border border-white/[0.05] rounded-xl px-3 py-2.5 text-primary text-sm focus:outline-none focus:border-gold-accent transition-colors placeholder:text-dim"
							/>
						</div>
						<div>
							<label for="wz-staff-phone" class="block text-xs font-medium text-muted mb-1.5">WhatsApp number</label>
							<input
								id="wz-staff-phone"
								bind:value={staffPhone}
								placeholder="9876543210"
								class="w-full bg-titanium border border-white/[0.05] rounded-xl px-3 py-2.5 text-primary text-sm focus:outline-none focus:border-gold-accent transition-colors placeholder:text-dim"
							/>
						</div>
						<div>
							<label for="wz-staff-role" class="block text-xs font-medium text-muted mb-1.5">Role</label>
							<select
								id="wz-staff-role"
								bind:value={staffRole}
								class="w-full bg-titanium border border-white/[0.05] rounded-xl px-3 py-2.5 text-primary text-sm focus:outline-none focus:border-gold-accent transition-colors"
							>
								<option value="barber">Barber</option>
								<option value="manager">Manager</option>
							</select>
						</div>
					</div>
					<div class="flex items-center justify-between mt-8">
						<button
							onclick={() => (wizardStep = 3)}
							class="text-muted hover:text-primary transition-colors text-sm">Skip</button
						>
						<button
							onclick={submitStaff}
							disabled={staffSubmitting}
							id="wz-add-staff-btn"
							class="bg-gold-accent hover:bg-gold-accent/90 disabled:opacity-40 text-canvas font-bold py-3 px-8 rounded-full transition-all duration-150 active:scale-[0.98]"
						>
							{staffSubmitting ? 'Saving…' : 'Continue'}
						</button>
					</div>
				{:else if wizardStep === 3}
					<h2 class="text-xl font-extrabold text-primary font-satoshi tracking-tightest mb-1">Connect WhatsApp</h2>
					<p class="text-muted text-sm mb-6">Optional — lets customers get queue updates on WhatsApp.</p>
					{#if waWebhookUrl}
						<div class="bg-emerald-950/20 border border-system-success/20 rounded-xl p-4 mb-4">
							<p class="text-system-success text-sm font-semibold mb-2">
								Connected. Paste this webhook URL into Bhejna Developer Settings:
							</p>
							<div class="flex gap-2">
								<input
									readonly
									value={waWebhookUrl}
									class="flex-1 bg-canvas border border-white/[0.05] rounded-lg px-3 py-2 text-system-success/80 text-xs font-mono focus:outline-none"
								/>
								<button
									onclick={copyWebhook}
									class="bg-titanium hover:bg-surface text-primary text-xs px-3 rounded-lg transition-colors border border-white/[0.05]"
								>
									{waCopied ? 'Copied' : 'Copy'}
								</button>
							</div>
						</div>
					{:else}
						{#if waError}
							<div class="text-sm mb-4 bg-red-950/30 border border-system-error/20 rounded-xl p-3 text-system-error/80">{waError}</div>
						{/if}
						<div>
							<label for="wz-wa-json" class="block text-xs font-medium text-muted mb-1.5">Bhejna config JSON</label>
							<textarea
								id="wz-wa-json"
								bind:value={waJson}
								rows="5"
								placeholder={`{"bhejna_config_version":"1","phone_number":"+91...","api_key":"...","webhook_secret":"...","whatsapp_status":"ACTIVE"}`}
								class="w-full bg-titanium border border-white/[0.05] rounded-xl px-3 py-2.5 text-primary text-xs font-mono focus:outline-none focus:border-gold-accent transition-colors resize-none placeholder:text-dim"
							></textarea>
						</div>
					{/if}
					<div class="flex items-center justify-between mt-8">
						<button
							onclick={() => (wizardDone = true)}
							class="text-muted hover:text-primary transition-colors text-sm"
						>
							{waWebhookUrl ? 'Continue' : 'Skip'}
						</button>
						{#if !waWebhookUrl}
							<button
								onclick={submitWhatsApp}
								disabled={waSubmitting}
								class="bg-gold-accent hover:bg-gold-accent/90 disabled:opacity-40 text-canvas font-bold py-3 px-8 rounded-full transition-all duration-150 active:scale-[0.98]"
							>
								{waSubmitting ? 'Connecting…' : 'Connect'}
							</button>
						{/if}
					</div>
				{/if}
			</div>

			<p class="text-center text-dim text-xs mt-6">You can change all of this later in settings.</p>
		</div>
	</div>
{:else}
	<!-- ===== NORMAL ADMIN HUB ===== -->
	<div class="min-h-screen bg-canvas">
		<div class="max-w-4xl mx-auto p-6">
			<div class="flex items-center justify-between mb-8">
				<div>
					<h1 class="text-2xl font-extrabold text-primary font-satoshi tracking-tightest">Admin</h1>
					<p class="text-muted text-sm mt-0.5">Manage your shop, services, and staff</p>
				</div>
				<a
					href="/dashboard"
					class="text-muted hover:text-primary transition-colors text-sm"
				>
					Back to Dashboard
				</a>
			</div>

			<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
				{#each [{ href: '/admin/services', label: 'Services', desc: 'Service catalog' }, { href: '/admin/staff', label: 'Staff', desc: 'Team members' }, { href: '/admin/shop', label: 'Shop Status', desc: 'Open or close the shop' }, { href: '/admin/whatsapp', label: 'WhatsApp', desc: 'Notification channel' }, { href: '/admin/analytics', label: 'Analytics', desc: 'Revenue and visits' }] as section}
					<a
						href={section.href}
						class="group bg-matte hover:bg-surface border border-white/[0.05] rounded-xl p-5 transition-all duration-150 active:scale-[0.98] machined-edge"
					>
						<h2 class="text-sm font-bold text-primary group-hover:text-gold-accent transition-colors">
							{section.label}
						</h2>
						<p class="text-xs text-muted mt-1">{section.desc}</p>
					</a>
				{/each}
			</div>
		</div>
	</div>
{/if}
