<script lang="ts">
	import { enhance } from '$app/forms';
	import Icon from '$lib/components/Icon.svelte';
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
	<meta
		name="description"
		content="Manage your service catalog: categories, groups, and variants"
	/>
</svelte:head>

<div class="min-h-screen bg-canvas">
	<div class="max-w-4xl mx-auto p-6">
		<!-- Header -->
		<div class="flex items-center justify-between mb-6">
			<div class="flex items-center gap-3">
				<a href="/admin" class="text-muted hover:text-primary transition-colors text-sm"
					>← Admin</a
				>
				<span class="text-dim">/</span>
				<h1 class="text-2xl font-bold text-primary">Services</h1>
			</div>
			<button
				id="toggle-create-form-btn"
				onclick={() => (showCreateForm = !showCreateForm)}
				class="bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] text-canvas font-bold px-4 py-2 rounded-xl text-sm transition-all duration-150"
			>
				{showCreateForm ? 'Cancel' : '+ Add Service'}
			</button>
		</div>

		{#if form?.error}
			<div class="bg-system-error/10 border border-system-error/30 rounded-xl p-4 mb-6 text-system-error text-sm">
				{form.error}
			</div>
		{/if}
		{#if form?.success}
			<div
				class="bg-system-success/10 border border-system-success/30 rounded-xl p-4 mb-6 text-system-success text-sm flex items-center gap-2"
			>
				<Icon name="check" size={14} /> Service updated successfully
			</div>
		{/if}

		<!-- Create Form -->
		{#if showCreateForm}
			<div class="bg-matte border border-white/[0.05] rounded-2xl p-6 mb-6 shadow-xl">
				<h2 class="text-lg font-bold text-primary mb-4">New Service Variant</h2>
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
							<label for="category_name" class="block text-xs text-muted mb-1"
								>Category Name *</label
							>
							<input
								id="category_name"
								name="category_name"
								bind:value={newCategoryName}
								required
								placeholder="Hair"
								class="w-full bg-titanium border border-white/[0.05] rounded-lg px-3 py-2 text-primary text-sm focus:outline-none focus:ring-2 focus:ring-gold-accent"
							/>
						</div>
						<div>
							<label for="category_gender" class="block text-xs text-muted mb-1">Gender</label>
							<select
								id="category_gender"
								name="category_gender"
								bind:value={newCategoryGender}
								class="w-full bg-titanium border border-white/[0.05] rounded-lg px-3 py-2 text-primary text-sm focus:outline-none focus:ring-2 focus:ring-gold-accent"
							>
								<option value="men">Men</option>
								<option value="women">Women</option>
								<option value="unisex">Unisex</option>
							</select>
						</div>
						<div>
							<label for="group_name" class="block text-xs text-muted mb-1">Group Name *</label>
							<input
								id="group_name"
								name="group_name"
								bind:value={newGroupName}
								required
								placeholder="Fade"
								class="w-full bg-titanium border border-white/[0.05] rounded-lg px-3 py-2 text-primary text-sm focus:outline-none focus:ring-2 focus:ring-gold-accent"
							/>
						</div>
						<div>
							<label for="variant_name" class="block text-xs text-muted mb-1"
								>Variant Name *</label
							>
							<input
								id="variant_name"
								name="variant_name"
								bind:value={newVariantName}
								required
								placeholder="Mid Fade"
								class="w-full bg-titanium border border-white/[0.05] rounded-lg px-3 py-2 text-primary text-sm focus:outline-none focus:ring-2 focus:ring-gold-accent"
							/>
						</div>
						<div>
							<label for="duration_minutes" class="block text-xs text-muted mb-1"
								>Duration (minutes) *</label
							>
							<input
								id="duration_minutes"
								name="duration_minutes"
								type="number"
								min="1"
								bind:value={newDuration}
								required
								placeholder="30"
								class="w-full bg-titanium border border-white/[0.05] rounded-lg px-3 py-2 text-primary text-sm focus:outline-none focus:ring-2 focus:ring-gold-accent"
							/>
						</div>
						<div>
							<label for="price_rupees" class="block text-xs text-muted mb-1"
								>Price (₹, whole number) *</label
							>
							<input
								id="price_rupees"
								name="price_rupees"
								type="number"
								min="0"
								step="1"
								bind:value={newPrice}
								required
								placeholder="150"
								class="w-full bg-titanium border border-white/[0.05] rounded-lg px-3 py-2 text-primary text-sm focus:outline-none focus:ring-2 focus:ring-gold-accent"
							/>
						</div>
					</div>
					<div class="flex flex-wrap gap-4 mb-4">
						<label class="flex items-center gap-2 text-sm text-primary cursor-pointer">
							<input
								type="checkbox"
								name="allow_walk_in"
								value="true"
								checked={newAllowWalkIn}
								onchange={(e) => (newAllowWalkIn = (e.target as HTMLInputElement).checked)}
								class="accent-gold-accent"
							/>
							Allow walk-in
						</label>
						<label class="flex items-center gap-2 text-sm text-primary cursor-pointer">
							<input
								type="checkbox"
								name="allow_appointment"
								value="true"
								checked={newAllowAppointment}
								onchange={(e) => (newAllowAppointment = (e.target as HTMLInputElement).checked)}
								class="accent-gold-accent"
							/>
							Allow appointment
						</label>
						<label class="flex items-center gap-2 text-sm text-primary cursor-pointer">
							<input
								type="checkbox"
								name="requires_appointment"
								value="true"
								checked={newRequiresAppointment}
								onchange={(e) => (newRequiresAppointment = (e.target as HTMLInputElement).checked)}
								class="accent-gold-accent"
							/>
							Requires appointment
						</label>
						<label class="flex items-center gap-2 text-sm text-primary cursor-pointer">
							<input
								type="checkbox"
								name="is_popular"
								value="true"
								checked={newIsPopular}
								onchange={(e) => (newIsPopular = (e.target as HTMLInputElement).checked)}
								class="accent-gold-accent"
							/>
							Popular
						</label>
					</div>
					<!-- Pass unchecked booleans as 'false' -->
					{#if !newAllowWalkIn}<input type="hidden" name="allow_walk_in" value="false" />{/if}
					{#if !newAllowAppointment}<input
							type="hidden"
							name="allow_appointment"
							value="false"
						/>{/if}
					<button
						id="submit-create-service-btn"
						type="submit"
						class="bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] text-canvas font-bold px-6 py-2 rounded-xl text-sm transition-all duration-150"
					>
						Create Variant
					</button>
				</form>
			</div>
		{/if}

		<!-- Catalog tree -->
		{#if !data.catalog.categories || data.catalog.categories.length === 0}
			<div class="bg-matte border border-white/[0.05] rounded-2xl p-12 text-center">
				<p class="text-muted text-lg mb-2">No services yet</p>
				<p class="text-dim text-sm">
					Click "+ Add Service" to create your first service variant.
				</p>
			</div>
		{:else}
			<div class="space-y-6">
				{#each data.catalog.categories as category}
					<div class="bg-matte border border-white/[0.05] rounded-2xl overflow-hidden">
						<!-- Category header -->
						<div
							class="bg-surface border-b border-white/[0.05] px-6 py-4 flex items-center justify-between"
						>
							<div>
								<h2 class="text-lg font-bold text-primary">{category.name}</h2>
								<span class="text-xs text-muted capitalize">{category.gender}</span>
							</div>
						</div>

						{#each category.groups as group}
							<div class="border-b border-white/[0.05] last:border-0">
								<div class="px-6 py-3 bg-matte/50">
									<h3 class="text-sm font-semibold text-gold-accent">{group.name}</h3>
								</div>
								<div class="overflow-x-auto"><table class="w-full">
									<thead>
										<tr class="text-left">
											<th class="px-6 py-2 text-xs text-dim font-medium">Variant</th>
											<th class="px-4 py-2 text-xs text-dim font-medium">Duration</th>
											<th class="px-4 py-2 text-xs text-dim font-medium">Price</th>
											<th class="px-4 py-2 text-xs text-dim font-medium">Flags</th>
											<th class="px-4 py-2 text-xs text-dim font-medium text-right"
												>Actions</th
											>
										</tr>
									</thead>
									<tbody>
										{#each group.variants as variant}
											<tr
												class="border-t border-white/[0.03] hover:bg-titanium/20 transition-colors"
											>
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
																<label class="block text-xs text-muted mb-1">Name</label>
																<input
																	name="variant_name"
																	value={variant.name}
																	class="w-full bg-titanium border border-white/[0.05] rounded-lg px-2 py-1.5 text-primary text-sm focus:outline-none focus:ring-1 focus:ring-gold-accent"
																/>
															</div>
															<div>
																<label class="block text-xs text-muted mb-1"
																	>Duration (min)</label
																>
																<input
																	name="duration_minutes"
																	type="number"
																	min="1"
																	value={variant.duration_minutes}
																	class="w-full bg-titanium border border-white/[0.05] rounded-lg px-2 py-1.5 text-primary text-sm focus:outline-none focus:ring-1 focus:ring-gold-accent"
																/>
															</div>
															<div>
																<label class="block text-xs text-muted mb-1">Price (₹)</label>
																<input
																	name="price_rupees"
																	type="number"
																	min="0"
																	step="1"
																	value={variant.price_paise / 100}
																	class="w-full bg-titanium border border-white/[0.05] rounded-lg px-2 py-1.5 text-primary text-sm focus:outline-none focus:ring-1 focus:ring-gold-accent"
																/>
															</div>
															<div>
																<label
																	class="flex items-center gap-1 text-xs text-muted mb-1 cursor-pointer"
																>
																	<input
																		type="checkbox"
																		name="is_popular"
																		value="true"
																		checked={variant.is_popular}
																		class="accent-gold-accent"
																	/> Popular
																</label>
																<div class="flex gap-2">
																	<button
																		type="submit"
																		class="bg-gold-accent hover:brightness-110 active:brightness-90 text-canvas font-bold px-3 py-1.5 rounded-lg text-xs transition-all duration-150"
																		>Save</button
																	>
																	<button
																		type="button"
																		onclick={() => (editingVariantId = null)}
																		class="text-muted hover:text-primary text-xs px-2 transition-colors"
																		>Cancel</button
																	>
																</div>
															</div>
														</form>
													</td>
												{:else}
													<td class="px-6 py-3 text-sm text-primary font-medium">
														{variant.name}
														{#if variant.is_popular}<span
																class="ml-1.5 font-mono text-[9px] font-medium uppercase tracking-wider text-gold-accent border border-gold-accent/30 bg-gold-accent/10 rounded px-1.5 py-0.5"
																>Popular</span
															>{/if}
													</td>
													<td class="px-4 py-3 text-sm text-primary font-mono"
														>{variant.duration_minutes} min</td
													>
													<td class="px-4 py-3 text-sm text-primary font-mono"
														>{formatPrice(variant.price_paise)}</td
													>
													<td class="px-4 py-3 text-xs text-muted">
														{#if variant.allow_walk_in}<span
																class="bg-titanium border border-white/[0.08] text-muted px-1.5 py-0.5 rounded mr-1"
																>Walk-in</span
															>{/if}
														{#if variant.allow_appointment}<span
																class="bg-titanium border border-white/[0.08] text-muted px-1.5 py-0.5 rounded mr-1"
																>Appt</span
															>{/if}
														{#if variant.requires_appointment}<span
																class="bg-system-warning/10 border border-system-warning/30 text-system-warning px-1.5 py-0.5 rounded"
																>Required</span
															>{/if}
													</td>
													<td class="px-4 py-3 text-right">
														<div class="flex items-center justify-end gap-2">
															<button
																onclick={() => (editingVariantId = variant.id)}
																class="text-muted hover:text-gold-accent text-xs transition-colors"
															>
																Edit
															</button>
															<form method="POST" action="?/deactivateVariant" use:enhance>
																<input type="hidden" name="variant_id" value={variant.id} />
																<button
																	type="submit"
																	class="text-dim hover:text-system-error text-xs transition-colors"
																	onclick={(e) => {
																		if (!confirm('Deactivate this service?')) e.preventDefault();
																	}}
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
								</table></div>
							</div>
						{/each}
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>
