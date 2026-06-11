<script lang="ts">
	import { enhance } from '$app/forms';
	import type { PageData, ActionData } from './$types';

	let { data, form }: { data: PageData; form: ActionData } = $props();

	let showCreateForm = $state(false);
	let editingVariantId = $state<string | null>(null);

	// Create form fields
	let newCategoryName = $state('');
	let newCategoryGender = $state<'men' | 'women' | 'unisex'>('unisex');
	let newGroupName = $state('');
	let newVariantName = $state('');
	let newDuration = $state('');
	let newPrice = $state('');
	let newAllowWalkIn = $state(true);
	let newAllowAppointment = $state(true);
	let newRequiresAppointment = $state(false);
	let newIsPopular = $state(false);

	function formatPrice(paise: number) {
		return `₹${(paise / 100).toLocaleString('en-IN')}`;
	}

	function resetCreate() {
		showCreateForm = false;
		newCategoryName = '';
		newGroupName = '';
		newVariantName = '';
		newDuration = '';
		newPrice = '';
	}
</script>

<svelte:head>
	<title>Services — Admin — BarberBase</title>
	<meta name="description" content="Manage your service catalog: categories, groups, and variants" />
</svelte:head>

<div class="min-h-screen bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
	<div class="max-w-4xl mx-auto p-6">
		<!-- Header -->
		<div class="flex items-center justify-between mb-6">
			<div class="flex items-center gap-3">
				<a href="/admin" class="text-slate-400 hover:text-white transition-colors text-sm">← Admin</a>
				<span class="text-slate-600">/</span>
				<h1 class="text-2xl font-bold text-white">Services</h1>
			</div>
			<button
				id="toggle-create-form-btn"
				onclick={() => (showCreateForm = !showCreateForm)}
				class="bg-amber-500 hover:bg-amber-400 text-slate-900 font-bold px-4 py-2 rounded-xl text-sm transition-all"
			>
				{showCreateForm ? '✕ Cancel' : '+ Add Service'}
			</button>
		</div>

		{#if form?.error}
			<div class="bg-red-900/30 border border-red-700 rounded-xl p-4 mb-6 text-red-400 text-sm">
				{form.error}
			</div>
		{/if}
		{#if form?.success}
			<div class="bg-green-900/30 border border-green-700 rounded-xl p-4 mb-6 text-green-400 text-sm">
				✓ Service updated successfully
			</div>
		{/if}

		<!-- Create Form -->
		{#if showCreateForm}
			<div class="bg-slate-800 border border-slate-700 rounded-2xl p-6 mb-6 shadow-xl">
				<h2 class="text-lg font-bold text-white mb-4">New Service Variant</h2>
				<form
					id="create-service-form"
					method="POST"
					action="?/createVariant"
					use:enhance={() => {
						return async ({ result, update }) => {
							if (result.type === 'success') resetCreate();
							await update();
						};
					}}
				>
					<div class="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-4">
						<div>
							<label for="category_name" class="block text-xs text-slate-400 mb-1">Category Name *</label>
							<input id="category_name" name="category_name" bind:value={newCategoryName} required placeholder="Hair" class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label for="category_gender" class="block text-xs text-slate-400 mb-1">Gender</label>
							<select id="category_gender" name="category_gender" bind:value={newCategoryGender} class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500">
								<option value="men">Men</option>
								<option value="women">Women</option>
								<option value="unisex">Unisex</option>
							</select>
						</div>
						<div>
							<label for="group_name" class="block text-xs text-slate-400 mb-1">Group Name *</label>
							<input id="group_name" name="group_name" bind:value={newGroupName} required placeholder="Fade" class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label for="variant_name" class="block text-xs text-slate-400 mb-1">Variant Name *</label>
							<input id="variant_name" name="variant_name" bind:value={newVariantName} required placeholder="Mid Fade" class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label for="duration_minutes" class="block text-xs text-slate-400 mb-1">Duration (minutes) *</label>
							<input id="duration_minutes" name="duration_minutes" type="number" min="1" bind:value={newDuration} required placeholder="30" class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label for="price_rupees" class="block text-xs text-slate-400 mb-1">Price (₹, whole number) *</label>
							<input id="price_rupees" name="price_rupees" type="number" min="0" step="1" bind:value={newPrice} required placeholder="150" class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
					</div>
					<div class="flex flex-wrap gap-4 mb-4">
						<label class="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
							<input type="checkbox" name="allow_walk_in" value="true" checked={newAllowWalkIn} onchange={(e) => (newAllowWalkIn = (e.target as HTMLInputElement).checked)} class="accent-amber-500" />
							Allow walk-in
						</label>
						<label class="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
							<input type="checkbox" name="allow_appointment" value="true" checked={newAllowAppointment} onchange={(e) => (newAllowAppointment = (e.target as HTMLInputElement).checked)} class="accent-amber-500" />
							Allow appointment
						</label>
						<label class="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
							<input type="checkbox" name="requires_appointment" value="true" checked={newRequiresAppointment} onchange={(e) => (newRequiresAppointment = (e.target as HTMLInputElement).checked)} class="accent-amber-500" />
							Requires appointment
						</label>
						<label class="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
							<input type="checkbox" name="is_popular" value="true" checked={newIsPopular} onchange={(e) => (newIsPopular = (e.target as HTMLInputElement).checked)} class="accent-amber-500" />
							⭐ Popular
						</label>
					</div>
					<!-- Pass unchecked booleans as 'false' -->
					{#if !newAllowWalkIn}<input type="hidden" name="allow_walk_in" value="false" />{/if}
					{#if !newAllowAppointment}<input type="hidden" name="allow_appointment" value="false" />{/if}
					<button id="submit-create-service-btn" type="submit" class="bg-amber-500 hover:bg-amber-400 text-slate-900 font-bold px-6 py-2 rounded-xl text-sm transition-all">
						Create Variant
					</button>
				</form>
			</div>
		{/if}

		<!-- Catalog tree -->
		{#if !data.catalog.categories || data.catalog.categories.length === 0}
			<div class="bg-slate-800 border border-slate-700 rounded-2xl p-12 text-center">
				<p class="text-slate-400 text-lg mb-2">No services yet</p>
				<p class="text-slate-500 text-sm">Click "+ Add Service" to create your first service variant.</p>
			</div>
		{:else}
			<div class="space-y-6">
				{#each data.catalog.categories as category}
					<div class="bg-slate-800 border border-slate-700 rounded-2xl overflow-hidden">
						<!-- Category header -->
						<div class="bg-slate-750 border-b border-slate-700 px-6 py-4 flex items-center justify-between">
							<div>
								<h2 class="text-lg font-bold text-white">{category.name}</h2>
								<span class="text-xs text-slate-400 capitalize">{category.gender}</span>
							</div>
						</div>

						{#each category.groups as group}
							<div class="border-b border-slate-700 last:border-0">
								<div class="px-6 py-3 bg-slate-800/50">
									<h3 class="text-sm font-semibold text-amber-400">{group.name}</h3>
								</div>
								<table class="w-full">
									<thead>
										<tr class="text-left">
											<th class="px-6 py-2 text-xs text-slate-500 font-medium">Variant</th>
											<th class="px-4 py-2 text-xs text-slate-500 font-medium">Duration</th>
											<th class="px-4 py-2 text-xs text-slate-500 font-medium">Price</th>
											<th class="px-4 py-2 text-xs text-slate-500 font-medium">Flags</th>
											<th class="px-4 py-2 text-xs text-slate-500 font-medium text-right">Actions</th>
										</tr>
									</thead>
									<tbody>
										{#each group.variants as variant}
											<tr class="border-t border-slate-700/50 hover:bg-slate-700/20 transition-colors">
												{#if editingVariantId === variant.id}
													<!-- Inline edit row -->
													<td colspan="5" class="px-6 py-4">
														<form
															method="POST"
															action="?/updateVariant"
															use:enhance={() => {
																return async ({ result, update }) => {
																	if (result.type === 'success') editingVariantId = null;
																	await update();
																};
															}}
															class="grid grid-cols-2 sm:grid-cols-4 gap-3 items-end"
														>
															<input type="hidden" name="variant_id" value={variant.id} />
															<div>
																<label class="block text-xs text-slate-400 mb-1">Name</label>
																<input name="variant_name" value={variant.name} class="w-full bg-slate-700 border border-slate-600 rounded-lg px-2 py-1.5 text-white text-sm focus:outline-none focus:ring-1 focus:ring-amber-500" />
															</div>
															<div>
																<label class="block text-xs text-slate-400 mb-1">Duration (min)</label>
																<input name="duration_minutes" type="number" min="1" value={variant.duration_minutes} class="w-full bg-slate-700 border border-slate-600 rounded-lg px-2 py-1.5 text-white text-sm focus:outline-none focus:ring-1 focus:ring-amber-500" />
															</div>
															<div>
																<label class="block text-xs text-slate-400 mb-1">Price (₹)</label>
																<input name="price_rupees" type="number" min="0" step="1" value={variant.price_paise / 100} class="w-full bg-slate-700 border border-slate-600 rounded-lg px-2 py-1.5 text-white text-sm focus:outline-none focus:ring-1 focus:ring-amber-500" />
															</div>
															<div>
																<label class="flex items-center gap-1 text-xs text-slate-400 mb-1 cursor-pointer">
																	<input type="checkbox" name="is_popular" value="true" checked={variant.is_popular} class="accent-amber-500" /> Popular
																</label>
																<div class="flex gap-2">
																	<button type="submit" class="bg-amber-500 hover:bg-amber-400 text-slate-900 font-bold px-3 py-1.5 rounded-lg text-xs transition-all">Save</button>
																	<button type="button" onclick={() => (editingVariantId = null)} class="text-slate-400 hover:text-white text-xs px-2 transition-colors">Cancel</button>
																</div>
															</div>
														</form>
													</td>
												{:else}
													<td class="px-6 py-3 text-sm text-white font-medium">
														{variant.name}
														{#if variant.is_popular}<span class="ml-1 text-amber-400 text-xs">⭐</span>{/if}
													</td>
													<td class="px-4 py-3 text-sm text-slate-300">{variant.duration_minutes} min</td>
													<td class="px-4 py-3 text-sm text-slate-300 font-mono">{formatPrice(variant.price_paise)}</td>
													<td class="px-4 py-3 text-xs text-slate-400">
														{#if variant.allow_walk_in}<span class="bg-blue-900/40 text-blue-400 px-1.5 py-0.5 rounded mr-1">Walk-in</span>{/if}
														{#if variant.allow_appointment}<span class="bg-purple-900/40 text-purple-400 px-1.5 py-0.5 rounded mr-1">Appt</span>{/if}
														{#if variant.requires_appointment}<span class="bg-orange-900/40 text-orange-400 px-1.5 py-0.5 rounded">Required</span>{/if}
													</td>
													<td class="px-4 py-3 text-right">
														<div class="flex items-center justify-end gap-2">
															<button
																onclick={() => (editingVariantId = variant.id)}
																class="text-slate-400 hover:text-amber-400 text-xs transition-colors"
															>
																Edit
															</button>
															<form method="POST" action="?/deactivateVariant" use:enhance>
																<input type="hidden" name="variant_id" value={variant.id} />
																<button
																	type="submit"
																	class="text-slate-500 hover:text-red-400 text-xs transition-colors"
																	onclick={(e) => { if (!confirm('Deactivate this service?')) e.preventDefault(); }}
																>
																	Deactivate
																</button>
															</form>
														</div>
													</td>
												{/if}
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						{/each}
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>
