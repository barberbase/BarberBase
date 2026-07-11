<script lang="ts">
	import { enhance } from '$app/forms';
	import Icon from '$lib/components/Icon.svelte';
	import type { PageData, ActionData } from './$types';

	let { data, form }: { data: PageData; form: ActionData } = $props();

	const isOwner = (data as any).staff?.role === 'owner';
	const settings = (data as any).settings;

	let routingMode = $state<string>((data as any).settings?.queue_routing_mode ?? 'pooled');
	let latitude = $state<string>((data as any).settings?.gps_latitude?.toString() ?? '');
	let longitude = $state<string>((data as any).settings?.gps_longitude?.toString() ?? '');
	let radius = $state<number>((data as any).settings?.arrival_radius_metres ?? 100);
	let geoAssist = $state<boolean>((data as any).settings?.geolocation_assist ?? true);

	let geoError = $state<string>('');
	let geoBusy = $state<boolean>(false);

	function useCurrentLocation() {
		geoError = '';
		if (!navigator.geolocation) {
			geoError = 'Geolocation is not supported by this browser.';
			return;
		}
		geoBusy = true;
		navigator.geolocation.getCurrentPosition(
			(pos) => {
				latitude = pos.coords.latitude.toFixed(7);
				longitude = pos.coords.longitude.toFixed(7);
				geoBusy = false;
			},
			(err) => {
				geoError =
					err.code === err.PERMISSION_DENIED
						? 'Location permission denied. Enter coordinates manually.'
						: 'Could not read location. Enter coordinates manually.';
				geoBusy = false;
			},
			{ enableHighAccuracy: true, timeout: 10000 }
		);
	}

	const routingOptions = [
		{
			value: 'pooled',
			label: 'Pooled',
			desc: 'One shared queue — next free barber takes the next customer. Customers never pick a barber.'
		},
		{
			value: 'hybrid',
			label: 'Hybrid',
			desc: 'Customers may request a specific barber, but "any available" is the default. Named requests wait for their barber.'
		},
		{
			value: 'barber_specific',
			label: 'Barber-specific',
			desc: 'Every customer must choose a barber. Each barber runs their own line — no shared pool.'
		}
	];
</script>

<svelte:head>
	<title>Settings — Admin — BarberBase</title>
	<meta name="description" content="Queue routing mode and arrival geofence settings" />
</svelte:head>

<div class="min-h-screen bg-canvas">
	<div class="max-w-2xl mx-auto p-6">
		<div class="flex items-center gap-3 mb-6">
			<a href="/admin" class="text-muted hover:text-primary transition-colors text-sm">← Admin</a>
			<span class="text-dim">/</span>
			<h1 class="text-2xl font-bold text-primary">Settings</h1>
		</div>

		{#if form?.error}
			<div
				class="bg-system-error/10 border border-system-error/30 rounded-xl p-4 mb-6 text-system-error text-sm"
			>
				{form.error}
			</div>
		{/if}
		{#if form?.success}
			<div
				class="bg-system-success/10 border border-system-success/30 rounded-xl p-4 mb-6 text-system-success text-sm flex items-center gap-2"
			>
				<Icon name="check" size={14} /> Settings saved
			</div>
		{/if}

		{#if !settings}
			<div class="bg-matte border border-white/[0.05] rounded-2xl p-6 text-muted text-sm">
				Could not load settings. Refresh to retry.
			</div>
		{:else}
			<!-- Queue routing mode — owner only, not rendered for managers -->
			{#if isOwner}
				<form method="POST" action="?/saveRouting" use:enhance>
					<div class="bg-matte border border-white/[0.05] rounded-2xl p-6 mb-6 shadow-xl">
						<h2 class="text-lg font-bold text-primary mb-1">Queue Routing</h2>
						<p class="text-xs text-muted mb-4">
							How customers are matched to barbers. This shapes staff dynamics — change it
							deliberately, not daily.
						</p>

						<div class="space-y-3">
							{#each routingOptions as opt}
								<label
									class="flex items-start gap-3 p-3 rounded-xl border cursor-pointer transition-colors {routingMode ===
									opt.value
										? 'border-gold-accent/40 bg-gold-accent/5'
										: 'border-white/[0.05] hover:border-white/[0.12]'}"
								>
									<input
										type="radio"
										name="queue_routing_mode"
										value={opt.value}
										bind:group={routingMode}
										class="mt-1 text-gold-accent"
									/>
									<span>
										<span class="block text-sm font-bold text-primary">{opt.label}</span>
										<span class="block text-xs text-muted mt-0.5">{opt.desc}</span>
									</span>
								</label>
							{/each}
						</div>

						<button
							type="submit"
							class="mt-4 bg-gold-accent text-canvas font-bold text-sm rounded-full px-5 py-2.5 hover:brightness-110 transition-all"
						>
							Save Routing Mode
						</button>
					</div>
				</form>
			{/if}

			<!-- Arrival geofence — owner or manager -->
			<form method="POST" action="?/saveGeofence" use:enhance>
				<div class="bg-matte border border-white/[0.05] rounded-2xl p-6 shadow-xl">
					<h2 class="text-lg font-bold text-primary mb-1">Arrival Geofence</h2>
					<p class="text-xs text-muted mb-4">
						Shop coordinates let customers auto-confirm arrival by GPS instead of typing the
						counter PIN.
					</p>

					<div class="grid grid-cols-2 gap-3 mb-3">
						<div>
							<label for="gps-lat" class="block text-xs font-medium text-muted mb-1">Latitude</label>
							<input
								id="gps-lat"
								name="gps_latitude"
								type="text"
								inputmode="decimal"
								placeholder="12.9716000"
								bind:value={latitude}
								class="w-full bg-canvas border border-white/[0.05] rounded-xl px-3 py-2 text-sm text-primary font-mono focus:outline-none focus:border-gold-accent"
							/>
						</div>
						<div>
							<label for="gps-lng" class="block text-xs font-medium text-muted mb-1">Longitude</label>
							<input
								id="gps-lng"
								name="gps_longitude"
								type="text"
								inputmode="decimal"
								placeholder="77.5946000"
								bind:value={longitude}
								class="w-full bg-canvas border border-white/[0.05] rounded-xl px-3 py-2 text-sm text-primary font-mono focus:outline-none focus:border-gold-accent"
							/>
						</div>
					</div>

					<button
						type="button"
						onclick={useCurrentLocation}
						disabled={geoBusy}
						class="text-xs text-gold-accent hover:text-primary transition-colors inline-flex items-center gap-1.5 mb-4 disabled:opacity-50"
					>
						<Icon name="map-pin" size={12} />
						{geoBusy ? 'Reading location…' : 'Use my current location'}
					</button>
					{#if geoError}
						<p class="text-xs text-system-error mb-3">{geoError}</p>
					{/if}

					<div class="mb-4">
						<label for="radius" class="block text-xs font-medium text-muted mb-1">
							Arrival radius — {radius} m
						</label>
						<input
							id="radius"
							name="arrival_radius_metres"
							type="range"
							min="20"
							max="500"
							step="10"
							bind:value={radius}
							class="w-full accent-[#C8A96B]"
						/>
						<div class="flex justify-between text-[10px] text-dim">
							<span>20 m</span>
							<span>500 m</span>
						</div>
					</div>

					<label class="flex items-center gap-3 mb-5 cursor-pointer">
						<input
							type="checkbox"
							name="geolocation_assist"
							bind:checked={geoAssist}
							class="rounded text-gold-accent bg-canvas border-white/[0.1]"
						/>
						<span class="text-sm text-primary">
							Offer GPS arrival confirmation
							<span class="block text-xs text-muted">
								When off, customers always use the counter PIN.
							</span>
						</span>
					</label>

					<button
						type="submit"
						class="bg-gold-accent text-canvas font-bold text-sm rounded-full px-5 py-2.5 hover:brightness-110 transition-all"
					>
						Save Geofence
					</button>
				</div>
			</form>
		{/if}
	</div>
</div>
