<script lang="ts">
	import { enhance } from '$app/forms';

	let { form } = $props<{
		form: {
			success?: boolean;
			tenant_id?: string;
			location_id?: string;
			owner_staff_member_id?: string;
			arrival_pin?: string;
			owner_phone?: string;
			public_path?: string;
			error?: string;
		};
	}>();

	// Form input states
	let tenantName = $state<string>('');
	let tenantSlug = $state<string>('');
	let ownerName = $state<string>('');
	let ownerPhone = $state<string>('');
	let locationName = $state<string>('');
	let locationSlug = $state<string>('');
	let address = $state<string>('');
	let timezone = $state<string>('Asia/Kolkata');

	// Edit flags to prevent auto-slugification from overwriting user edits
	let isTenantSlugEdited = $state<boolean>(false);
	let isLocationSlugEdited = $state<boolean>(false);

	let loading = $state<boolean>(false);
	let copiedMap = $state<Record<string, boolean>>({});

	// Helper to slugify strings
	function slugify(text: string): string {
		return text
			.toLowerCase()
			.replace(/\s+/g, '-')
			.replace(/[^a-z0-9-]/g, '')
			.replace(/--+/g, '-')
			.replace(/^-+/, '')
			.replace(/-+$/, '');
	}

	// Auto-slugify tenant_name to tenant_slug
	$effect(() => {
		if (!isTenantSlugEdited && tenantName) {
			tenantSlug = slugify(tenantName);
		}
	});

	// Auto-slugify location_name and prefix with tenant_slug
	$effect(() => {
		if (!isLocationSlugEdited && locationName) {
			const suffix = slugify(locationName);
			locationSlug = tenantSlug ? `${tenantSlug}/${suffix}` : suffix;
		}
	});

	// Keep location prefix up to date if tenant slug changes
	$effect(() => {
		if (!isLocationSlugEdited && tenantSlug && locationSlug) {
			const parts = locationSlug.split('/');
			const suffix = parts.length > 1 ? parts.slice(1).join('/') : parts[0];
			locationSlug = `${tenantSlug}/${suffix}`;
		}
	});

	// Handle ID and text copying
	function handleCopy(text: string, key: string) {
		if (navigator.clipboard) {
			navigator.clipboard.writeText(text);
			copiedMap[key] = true;
			setTimeout(() => {
				copiedMap[key] = false;
			}, 2000);
		}
	}

	// Reset form state for another provisioning run
	function handleReset() {
		tenantName = '';
		tenantSlug = '';
		ownerName = '';
		ownerPhone = '';
		locationName = '';
		locationSlug = '';
		address = '';
		timezone = 'Asia/Kolkata';
		isTenantSlugEdited = false;
		isLocationSlugEdited = false;
		if (form) {
			form.success = false;
			form.error = undefined;
		}
	}
</script>

<svelte:head>
	<title>Operator Console — BarberBase</title>
</svelte:head>

<div
	class="min-h-screen bg-canvas text-primary flex flex-col font-manrope"
>
	<!-- Top Navigation Header -->
	<header
		class="bg-matte border-b border-white/[0.03] px-6 py-4 flex justify-between items-center"
	>
		<div class="flex items-center space-x-3">
			<span class="text-xl font-extrabold text-gold-accent tracking-wider">BarberBase</span>
			<span class="text-dim">|</span>
			<span class="text-sm font-semibold text-primary">Operator Console</span>
		</div>
		<div
			class="text-xs font-bold uppercase tracking-wider px-3 py-1 rounded-full border border-white/[0.05] bg-slate-800 text-primary"
		>
			Authorized Session
		</div>
	</header>

	<main class="flex-1 max-w-4xl w-full mx-auto p-6 flex flex-col justify-center">
		{#if form?.success}
			<!-- SUCCESS PANEL -->
			<div
				class="bg-matte border border-white/[0.03] rounded-3xl p-8 shadow-2xl space-y-8 animate-fade-in"
			>
				<div class="text-center space-y-2">
					<div
						class="mx-auto w-16 h-16 rounded-full bg-system-success/10 border border-system-success/30 flex items-center justify-center text-3xl"
					>
						🎉
					</div>
					<h2 class="text-2xl font-extrabold text-system-success/80">Shop Successfully Provisioned!</h2>
					<p class="text-sm text-muted">
						Write down these credentials. The database records have been bootstrapped.
					</p>
				</div>

				<div class="grid grid-cols-1 md:grid-cols-2 gap-6">
					<!-- Pin display (Critical UX) -->
					<div
						class="bg-canvas border border-white/[0.03] rounded-2xl p-6 flex flex-col items-center justify-center text-center space-y-2"
					>
						<span class="text-xs font-bold text-dim uppercase tracking-widest"
							>Arrival PIN</span
						>
						<span class="text-4xl font-black text-gold-accent tracking-widest"
							>{form.arrival_pin}</span
						>
						<span
							class="text-xs font-bold text-gold-accent/80 bg-gold-accent/5 border border-gold-accent/10 px-3 py-1 rounded-full"
						>
							Shown once — laminate on the counter
						</span>
					</div>

					<!-- Onboarding details -->
					<div
						class="bg-canvas border border-white/[0.03] rounded-2xl p-6 flex flex-col justify-center space-y-3"
					>
						<span class="text-xs font-bold text-dim uppercase tracking-widest"
							>Shop Domain</span
						>
						<a
							href="https://barberbase.in{form.public_path}"
							target="_blank"
							class="text-sm font-bold text-gold-accent hover:text-gold-accent/80 underline break-all flex items-center space-x-1.5"
						>
							<span>barberbase.in{form.public_path}</span>
							<svg
								xmlns="http://www.w3.org/2000/svg"
								class="h-4 w-4 shrink-0"
								fill="none"
								viewBox="0 0 24 24"
								stroke="currentColor"
							>
								<path
									stroke-linecap="round"
									stroke-linejoin="round"
									stroke-width="2"
									d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14"
								/>
							</svg>
						</a>
						<span class="text-xs text-muted mt-1 block">
							Owner can now log in at <a href="/login" class="text-gold-accent hover:underline"
								>/login</a
							>
							with <span class="font-bold text-primary">{form.owner_phone}</span>
						</span>
					</div>
				</div>

				<!-- Identity mappings -->
				<div class="bg-canvas border border-white/[0.03] rounded-2xl p-6 space-y-4">
					<span
						class="text-xs font-bold text-muted uppercase tracking-widest block border-b border-white/[0.03] pb-2"
					>
						System Identifiers
					</span>

					<div class="space-y-3">
						<div class="flex justify-between items-center gap-4">
							<span class="text-xs text-dim font-semibold">Tenant ID</span>
							<div class="flex items-center space-x-2">
								<code
									class="text-xs font-mono text-primary bg-matte border border-white/[0.03] px-2.5 py-1.5 rounded-lg select-all"
								>
									{form.tenant_id}
								</code>
								<button
									type="button"
									onclick={() => handleCopy(form.tenant_id || '', 'tenant')}
									class="px-2.5 py-1.5 text-[10px] font-bold rounded-lg border border-white/[0.03] hover:bg-matte transition-colors shrink-0"
								>
									{copiedMap['tenant'] ? 'Copied' : 'Copy'}
								</button>
							</div>
						</div>

						<div class="flex justify-between items-center gap-4">
							<span class="text-xs text-dim font-semibold">Location ID</span>
							<div class="flex items-center space-x-2">
								<code
									class="text-xs font-mono text-primary bg-matte border border-white/[0.03] px-2.5 py-1.5 rounded-lg select-all"
								>
									{form.location_id}
								</code>
								<button
									type="button"
									onclick={() => handleCopy(form.location_id || '', 'location')}
									class="px-2.5 py-1.5 text-[10px] font-bold rounded-lg border border-white/[0.03] hover:bg-matte transition-colors shrink-0"
								>
									{copiedMap['location'] ? 'Copied' : 'Copy'}
								</button>
							</div>
						</div>

						<div class="flex justify-between items-center gap-4">
							<span class="text-xs text-dim font-semibold">Owner Staff ID</span>
							<div class="flex items-center space-x-2">
								<code
									class="text-xs font-mono text-primary bg-matte border border-white/[0.03] px-2.5 py-1.5 rounded-lg select-all"
								>
									{form.owner_staff_member_id}
								</code>
								<button
									type="button"
									onclick={() => handleCopy(form.owner_staff_member_id || '', 'owner')}
									class="px-2.5 py-1.5 text-[10px] font-bold rounded-lg border border-white/[0.03] hover:bg-matte transition-colors shrink-0"
								>
									{copiedMap['owner'] ? 'Copied' : 'Copy'}
								</button>
							</div>
						</div>
					</div>
				</div>

				<button
					type="button"
					onclick={handleReset}
					class="w-full py-4 bg-slate-800 hover:bg-slate-700 active:bg-slate-650 text-primary font-bold text-base rounded-2xl transition-colors cursor-pointer text-center"
				>
					Provision Another Shop
				</button>
			</div>
		{:else}
			<!-- PROVISIONING FORM -->
			<div
				class="bg-matte border border-white/[0.03] rounded-3xl p-8 shadow-2xl space-y-8 animate-fade-in"
			>
				<div>
					<h2 class="text-2xl font-extrabold text-primary">Onboard New Salon / Shop</h2>
					<p class="text-sm text-muted">
						Instantly provision database tables, location attributes, and WhatsApp router rules.
					</p>
				</div>

				<form
					method="POST"
					action="?/provision"
					use:enhance={() => {
						loading = true;
						return async ({ update }) => {
							await update();
							loading = false;
						};
					}}
					class="space-y-6"
				>
					<!-- Tenant section -->
					<div class="space-y-4">
						<h3
							class="text-xs font-bold text-gold-accent uppercase tracking-wider border-b border-white/[0.03] pb-2"
						>
							1. Tenant Company Info
						</h3>

						<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
							<div class="space-y-1.5">
								<label for="tenant_name" class="block text-xs font-medium text-slate-450">
									Tenant Company Name
								</label>
								<input
									type="text"
									id="tenant_name"
									name="tenant_name"
									placeholder="e.g. Star Salon"
									required
									disabled={loading}
									bind:value={tenantName}
									class="w-full bg-canvas border border-white/[0.03] rounded-xl px-4 py-3 text-primary focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-amber-500 text-sm"
								/>
							</div>

							<div class="space-y-1.5">
								<label for="tenant_slug" class="block text-xs font-medium text-slate-450">
									Tenant URL Slug
								</label>
								<input
									type="text"
									id="tenant_slug"
									name="tenant_slug"
									placeholder="e.g. star-salon"
									required
									disabled={loading}
									bind:value={tenantSlug}
									oninput={() => (isTenantSlugEdited = true)}
									class="w-full bg-canvas border border-white/[0.03] rounded-xl px-4 py-3 text-primary focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-amber-500 text-sm"
								/>
							</div>
						</div>
					</div>

					<!-- Location section -->
					<div class="space-y-4">
						<h3
							class="text-xs font-bold text-gold-accent uppercase tracking-wider border-b border-white/[0.03] pb-2"
						>
							2. First Location Info
						</h3>

						<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
							<div class="space-y-1.5">
								<label for="location_name" class="block text-xs font-medium text-slate-450">
									Location Name
								</label>
								<input
									type="text"
									id="location_name"
									name="location_name"
									placeholder="e.g. Star Salon — Koramangala"
									required
									disabled={loading}
									bind:value={locationName}
									class="w-full bg-canvas border border-white/[0.03] rounded-xl px-4 py-3 text-primary focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-amber-500 text-sm"
								/>
							</div>

							<div class="space-y-1.5">
								<label for="location_slug" class="block text-xs font-medium text-slate-450">
									Location Full URL Slug
								</label>
								<input
									type="text"
									id="location_slug"
									name="location_slug"
									placeholder="e.g. star-salon/koramangala"
									required
									disabled={loading}
									bind:value={locationSlug}
									oninput={() => (isLocationSlugEdited = true)}
									class="w-full bg-canvas border border-white/[0.03] rounded-xl px-4 py-3 text-primary focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-amber-500 text-sm"
								/>
							</div>
						</div>

						<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
							<div class="space-y-1.5">
								<label for="address" class="block text-xs font-medium text-slate-450">
									Physical Address (Optional)
								</label>
								<input
									type="text"
									id="address"
									name="address"
									placeholder="e.g. 12, 80 Feet Road, Koramangala"
									disabled={loading}
									bind:value={address}
									class="w-full bg-canvas border border-white/[0.03] rounded-xl px-4 py-3 text-primary focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-amber-500 text-sm"
								/>
							</div>

							<div class="space-y-1.5">
								<label for="timezone" class="block text-xs font-medium text-slate-450">
									Location Timezone
								</label>
								<select
									id="timezone"
									name="timezone"
									disabled={loading}
									bind:value={timezone}
									class="w-full bg-canvas border border-white/[0.03] rounded-xl px-4 py-3 text-primary focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-amber-500 text-sm"
								>
									<option value="Asia/Kolkata">Asia/Kolkata (India Standard Time)</option>
									<option value="UTC">UTC (Coordinated Universal Time)</option>
								</select>
							</div>
						</div>
					</div>

					<!-- Owner section -->
					<div class="space-y-4">
						<h3
							class="text-xs font-bold text-gold-accent uppercase tracking-wider border-b border-white/[0.03] pb-2"
						>
							3. Owner Staff Profile
						</h3>

						<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
							<div class="space-y-1.5">
								<label for="owner_name" class="block text-xs font-medium text-slate-450">
									Owner Full Name
								</label>
								<input
									type="text"
									id="owner_name"
									name="owner_name"
									placeholder="e.g. Ravi Kumar"
									required
									disabled={loading}
									bind:value={ownerName}
									class="w-full bg-canvas border border-white/[0.03] rounded-xl px-4 py-3 text-primary focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-amber-500 text-sm"
								/>
							</div>

							<div class="space-y-1.5">
								<label for="owner_phone" class="block text-xs font-medium text-slate-450">
									Owner WhatsApp Phone Number
								</label>
								<input
									type="tel"
									id="owner_phone"
									name="owner_phone"
									placeholder="e.g. 9876543210 or +919876543210"
									required
									disabled={loading}
									bind:value={ownerPhone}
									class="w-full bg-canvas border border-white/[0.03] rounded-xl px-4 py-3 text-primary focus:outline-none focus:border-gold-accent focus:ring-1 focus:ring-amber-500 text-sm"
								/>
							</div>
						</div>
					</div>

					<!-- Inline Error Display -->
					{#if form?.error}
						<div
							class="bg-red-950/30 border border-system-error/30 rounded-2xl p-4 text-sm text-system-error/80 flex items-start space-x-3 animate-fade-in"
						>
							<svg
								xmlns="http://www.w3.org/2000/svg"
								class="h-5 w-5 shrink-0 mt-0.5"
								fill="none"
								viewBox="0 0 24 24"
								stroke="currentColor"
							>
								<path
									stroke-linecap="round"
									stroke-linejoin="round"
									stroke-width="2"
									d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
								/>
							</svg>
							<div>{form.error}</div>
						</div>
					{/if}

					<button
						type="submit"
						disabled={loading}
						class="w-full py-4 bg-gold-accent hover:bg-amber-400 active:bg-amber-600 disabled:opacity-40 disabled:hover:bg-gold-accent text-canvas font-bold text-base rounded-2xl transition-all duration-150 shadow-lg cursor-pointer flex items-center justify-center space-x-2"
					>
						{#if loading}
							<svg
								class="animate-spin h-5 w-5 text-slate-950"
								xmlns="http://www.w3.org/2000/svg"
								fill="none"
								viewBox="0 0 24 24"
							>
								<circle
									class="opacity-25"
									cx="12"
									cy="12"
									r="10"
									stroke="currentColor"
									stroke-width="4"
								></circle>
								<path
									class="opacity-75"
									fill="currentColor"
									d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
								></path>
							</svg>
							<span>Provisioning System...</span>
						{:else}
							<span>Bootstrap Shop & Owner</span>
						{/if}
					</button>
				</form>
			</div>
		{/if}
	</main>
</div>

<style>
	@keyframes fadeIn {
		from {
			opacity: 0;
			transform: translateY(4px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}
	.animate-fade-in {
		animation: fadeIn 0.2s ease-out forwards;
	}
</style>
