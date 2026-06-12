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
	<div
		class="min-h-screen bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900 flex items-center justify-center p-4"
	>
		<div class="w-full max-w-lg">
			<!-- Progress -->
			<div class="mb-8 flex items-center gap-3">
				{#each [1, 2, 3] as s}
					<div class="flex items-center gap-2 flex-1">
						<div
							class="w-8 h-8 rounded-full flex items-center justify-center text-sm font-bold transition-all {wizardStep >=
							s
								? 'bg-amber-500 text-slate-900'
								: 'bg-slate-700 text-slate-400'}"
						>
							{s}
						</div>
						{#if s < 3}
							<div class="flex-1 h-0.5 {wizardStep > s ? 'bg-amber-500' : 'bg-slate-700'}"></div>
						{/if}
					</div>
				{/each}
			</div>

			<div class="bg-slate-800 border border-slate-700 rounded-2xl p-8 shadow-2xl">
				{#if wizardStep === 1}
					<h1 class="text-2xl font-bold text-white mb-2">Welcome to BarberBase! 🎉</h1>
					<p class="text-slate-400 mb-6 text-sm">
						Step 1: Add your first service so customers can book with you.
					</p>
					{#if svcError}
						<p class="text-red-400 text-sm mb-4 bg-red-900/20 rounded-lg p-3">{svcError}</p>
					{/if}
					<div class="space-y-4">
						<div class="grid grid-cols-2 gap-3">
							<div>
								<label for="wz-cat-name" class="block text-xs text-slate-400 mb-1"
									>Category (e.g. Hair)</label
								>
								<input
									id="wz-cat-name"
									bind:value={svcCategoryName}
									placeholder="Hair"
									class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500"
								/>
							</div>
							<div>
								<label for="wz-cat-gender" class="block text-xs text-slate-400 mb-1">Gender</label>
								<select
									id="wz-cat-gender"
									bind:value={svcCategoryGender}
									class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500"
								>
									<option value="men">Men</option>
									<option value="women">Women</option>
									<option value="unisex">Unisex</option>
								</select>
							</div>
						</div>
						<div>
							<label for="wz-group" class="block text-xs text-slate-400 mb-1"
								>Group (e.g. Fade)</label
							>
							<input
								id="wz-group"
								bind:value={svcGroupName}
								placeholder="Fade"
								class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500"
							/>
						</div>
						<div>
							<label for="wz-variant" class="block text-xs text-slate-400 mb-1"
								>Variant name (e.g. Mid Fade)</label
							>
							<input
								id="wz-variant"
								bind:value={svcVariantName}
								placeholder="Mid Fade"
								class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500"
							/>
						</div>
						<div class="grid grid-cols-2 gap-3">
							<div>
								<label for="wz-duration" class="block text-xs text-slate-400 mb-1"
									>Duration (min)</label
								>
								<input
									id="wz-duration"
									type="number"
									min="1"
									bind:value={svcDuration}
									placeholder="30"
									class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500"
								/>
							</div>
							<div>
								<label for="wz-price" class="block text-xs text-slate-400 mb-1">Price (₹)</label>
								<input
									id="wz-price"
									type="number"
									min="0"
									step="1"
									bind:value={svcPrice}
									placeholder="150"
									class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500"
								/>
							</div>
						</div>
					</div>
					<div class="flex gap-3 mt-6">
						<button
							onclick={submitService}
							disabled={svcSubmitting}
							id="wz-add-service-btn"
							class="flex-1 bg-amber-500 hover:bg-amber-400 disabled:opacity-50 text-slate-900 font-bold py-3 rounded-xl transition-all"
						>
							{svcSubmitting ? 'Saving…' : 'Add Service →'}
						</button>
						<button
							onclick={() => (wizardStep = 2)}
							class="px-4 text-slate-400 hover:text-white transition-colors text-sm">Skip</button
						>
					</div>
				{:else if wizardStep === 2}
					<h2 class="text-2xl font-bold text-white mb-2">Add your first staff member</h2>
					<p class="text-slate-400 mb-6 text-sm">
						Step 2: Add a barber or manager who can log in and take customers.
					</p>
					{#if staffError}
						<p class="text-red-400 text-sm mb-4 bg-red-900/20 rounded-lg p-3">{staffError}</p>
					{/if}
					<div class="space-y-4">
						<div>
							<label for="wz-staff-name" class="block text-xs text-slate-400 mb-1">Full name</label>
							<input
								id="wz-staff-name"
								bind:value={staffName}
								placeholder="Ravi Kumar"
								class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500"
							/>
						</div>
						<div>
							<label for="wz-staff-phone" class="block text-xs text-slate-400 mb-1"
								>WhatsApp number</label
							>
							<input
								id="wz-staff-phone"
								bind:value={staffPhone}
								placeholder="9876543210"
								class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500"
							/>
						</div>
						<div>
							<label for="wz-staff-role" class="block text-xs text-slate-400 mb-1">Role</label>
							<select
								id="wz-staff-role"
								bind:value={staffRole}
								class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500"
							>
								<option value="barber">Barber</option>
								<option value="manager">Manager</option>
							</select>
						</div>
					</div>
					<div class="flex gap-3 mt-6">
						<button
							onclick={submitStaff}
							disabled={staffSubmitting}
							id="wz-add-staff-btn"
							class="flex-1 bg-amber-500 hover:bg-amber-400 disabled:opacity-50 text-slate-900 font-bold py-3 rounded-xl transition-all"
						>
							{staffSubmitting ? 'Saving…' : 'Add Staff Member →'}
						</button>
						<button
							onclick={() => (wizardStep = 3)}
							class="px-4 text-slate-400 hover:text-white transition-colors text-sm">Skip</button
						>
					</div>
				{:else if wizardStep === 3}
					<h2 class="text-2xl font-bold text-white mb-2">
						Connect WhatsApp <span class="text-slate-400 text-lg font-normal">(optional)</span>
					</h2>
					<p class="text-slate-400 mb-6 text-sm">
						Step 3: Use your own WhatsApp number so customers see your shop name as the sender.
					</p>
					{#if waWebhookUrl}
						<div class="bg-green-900/30 border border-green-700 rounded-xl p-4 mb-4">
							<p class="text-green-400 text-sm font-semibold mb-2">
								✓ Connected! Paste this URL into Bhejna → Developer Settings → Webhook URL:
							</p>
							<div class="flex gap-2">
								<input
									readonly
									value={waWebhookUrl}
									class="flex-1 bg-slate-900 rounded-lg px-3 py-2 text-green-300 text-xs font-mono focus:outline-none"
								/>
								<button
									onclick={copyWebhook}
									class="bg-slate-700 hover:bg-slate-600 text-white text-xs px-3 rounded-lg transition-colors"
								>
									{waCopied ? '✓ Copied' : 'Copy'}
								</button>
							</div>
						</div>
					{:else}
						{#if waError}
							<p class="text-red-400 text-sm mb-4 bg-red-900/20 rounded-lg p-3">{waError}</p>
						{/if}
						<div>
							<label for="wz-wa-json" class="block text-xs text-slate-400 mb-1"
								>Paste Bhejna Integration Config JSON</label
							>
							<textarea
								id="wz-wa-json"
								bind:value={waJson}
								rows="6"
								placeholder={`{"bhejna_config_version":"1","phone_number":"+91...","api_key":"...","webhook_secret":"...","whatsapp_status":"ACTIVE"}`}
								class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-xs font-mono focus:outline-none focus:ring-2 focus:ring-amber-500 resize-none"
							></textarea>
						</div>
					{/if}
					<div class="flex gap-3 mt-6">
						{#if !waWebhookUrl}
							<button
								onclick={submitWhatsApp}
								disabled={waSubmitting}
								class="flex-1 bg-amber-500 hover:bg-amber-400 disabled:opacity-50 text-slate-900 font-bold py-3 rounded-xl transition-all"
							>
								{waSubmitting ? 'Connecting…' : 'Connect WhatsApp →'}
							</button>
						{/if}
						<button
							onclick={() => (wizardDone = true)}
							class="px-4 text-slate-400 hover:text-white transition-colors text-sm"
						>
							{waWebhookUrl ? 'Continue →' : 'Skip'}
						</button>
					</div>
				{/if}
			</div>
		</div>
	</div>
{:else}
	<!-- ===== NORMAL ADMIN HUB ===== -->
	<div class="min-h-screen bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
		<div class="max-w-4xl mx-auto p-6">
			<div class="flex items-center justify-between mb-8">
				<div>
					<h1 class="text-3xl font-bold text-white">Admin Panel</h1>
					<p class="text-slate-400 mt-1">Manage your shop, services, and staff</p>
				</div>
				<a
					href="/dashboard"
					class="flex items-center gap-2 text-slate-400 hover:text-white transition-colors text-sm"
				>
					<span>←</span> Back to Dashboard
				</a>
			</div>

			<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
				{#each [{ href: '/admin/services', emoji: '✂️', label: 'Services', desc: 'CRUD service catalog', color: 'amber' }, { href: '/admin/staff', emoji: '👥', label: 'Staff', desc: 'Manage team members', color: 'blue' }, { href: '/admin/shop', emoji: '🏪', label: 'Shop Status', desc: 'Open / close the shop', color: 'green' }, { href: '/admin/whatsapp', emoji: '💬', label: 'WhatsApp', desc: 'Connect your number', color: 'emerald' }, { href: '/admin/analytics', emoji: '📊', label: 'Analytics', desc: 'Daily revenue & visits', color: 'violet' }] as section}
					<a
						href={section.href}
						class="group bg-slate-800 hover:bg-slate-700 border border-slate-700 hover:border-slate-500 rounded-2xl p-6 transition-all duration-200 hover:shadow-xl hover:-translate-y-0.5"
					>
						<div class="text-3xl mb-3">{section.emoji}</div>
						<h2 class="text-lg font-bold text-white group-hover:text-amber-400 transition-colors">
							{section.label}
						</h2>
						<p class="text-sm text-slate-400 mt-1">{section.desc}</p>
					</a>
				{/each}
			</div>
		</div>
	</div>
{/if}
