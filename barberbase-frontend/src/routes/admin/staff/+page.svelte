<script lang="ts">
	import { enhance } from '$app/forms';
	import type { PageData, ActionData } from './$types';

	let { data, form }: { data: PageData; form: ActionData } = $props();

	let showAddForm = $state(false);

	const roleLabel = (r: string) =>
		({ owner: '👑 Owner', manager: '🔑 Manager', barber: '✂️ Barber' })[r] || r;
	const statusLabel = (s: string) =>
		({ idle: '🟢 Idle', cutting: '✂️ Cutting', break: '⏸ Break', offline: '⚫ Offline' })[s] || s;
</script>

<svelte:head>
	<title>Staff — Admin — BarberBase</title>
	<meta name="description" content="Manage your team: add barbers and managers" />
</svelte:head>

<div class="min-h-screen bg-canvas">
	<div class="max-w-3xl mx-auto p-6">
		<!-- Header -->
		<div class="flex items-center justify-between mb-6">
			<div class="flex items-center gap-3">
				<a href="/admin" class="text-muted hover:text-primary transition-colors text-sm"
					>← Admin</a
				>
				<span class="text-dim">/</span>
				<h1 class="text-2xl font-bold text-primary">Staff</h1>
			</div>
			<button
				id="toggle-add-staff-btn"
				onclick={() => (showAddForm = !showAddForm)}
				class="bg-gold-accent hover:bg-amber-400 text-canvas font-bold px-4 py-2 rounded-xl text-sm transition-all"
			>
				{showAddForm ? '✕ Cancel' : '+ Add Staff'}
			</button>
		</div>

		{#if form?.error}
			<div class="bg-red-900/30 border border-red-700 rounded-xl p-4 mb-6 text-system-error/80 text-sm">
				{form.error}
			</div>
		{/if}
		{#if form?.success}
			<div
				class="bg-green-900/30 border border-green-700 rounded-xl p-4 mb-6 text-green-400 text-sm"
			>
				✓ Staff member added successfully
			</div>
		{/if}

		<!-- Add Staff Form -->
		{#if showAddForm}
			<div class="bg-matte border border-white/[0.05] rounded-2xl p-6 mb-6 shadow-xl">
				<h2 class="text-lg font-bold text-primary mb-4">Add Staff Member</h2>
				<form
					id="add-staff-form"
					method="POST"
					action="?/addMember"
					use:enhance={() => {
						return async ({ result, update }) => {
							if (result.type === 'success') showAddForm = false;
							await update();
						};
					}}
				>
					<div class="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-4">
						<div>
							<label for="staff-name" class="block text-xs text-muted mb-1">Full Name *</label>
							<input
								id="staff-name"
								name="name"
								required
								placeholder="Ravi Kumar"
								class="w-full bg-titanium border border-white/[0.05] rounded-lg px-3 py-2 text-primary text-sm focus:outline-none focus:ring-2 focus:ring-gold-accent"
							/>
						</div>
						<div>
							<label for="staff-phone" class="block text-xs text-muted mb-1"
								>WhatsApp Number *</label
							>
							<input
								id="staff-phone"
								name="phone_number"
								required
								placeholder="9876543210"
								class="w-full bg-titanium border border-white/[0.05] rounded-lg px-3 py-2 text-primary text-sm focus:outline-none focus:ring-2 focus:ring-gold-accent"
							/>
							<p class="text-xs text-dim mt-1">Will prepend +91 if not provided</p>
						</div>
						<div>
							<label for="staff-role" class="block text-xs text-muted mb-1">Role *</label>
							<select
								id="staff-role"
								name="role"
								class="w-full bg-titanium border border-white/[0.05] rounded-lg px-3 py-2 text-primary text-sm focus:outline-none focus:ring-2 focus:ring-gold-accent"
							>
								<option value="barber">Barber</option>
								<option value="manager">Manager</option>
							</select>
						</div>
					</div>
					<button
						id="submit-add-staff-btn"
						type="submit"
						class="bg-gold-accent hover:bg-amber-400 text-canvas font-bold px-6 py-2 rounded-xl text-sm transition-all"
					>
						Add Staff Member
					</button>
				</form>
			</div>
		{/if}

		<!-- Staff Table -->
		{#if data.staffMembers.length === 0}
			<div class="bg-matte border border-white/[0.05] rounded-2xl p-12 text-center">
				<p class="text-muted text-lg mb-2">No staff members yet</p>
				<p class="text-dim text-sm">Click "+ Add Staff" to add your first team member.</p>
			</div>
		{:else}
			<div class="bg-matte border border-white/[0.05] rounded-2xl overflow-hidden shadow-xl">
				<table class="w-full">
					<thead>
						<tr class="border-b border-white/[0.05]">
							<th
								class="px-6 py-4 text-left text-xs text-muted font-medium uppercase tracking-wider"
								>Name</th
							>
							<th
								class="px-4 py-4 text-left text-xs text-muted font-medium uppercase tracking-wider"
								>Role</th
							>
							<th
								class="px-4 py-4 text-left text-xs text-muted font-medium uppercase tracking-wider"
								>Status</th
							>
						</tr>
					</thead>
					<tbody class="divide-y divide-white/[0.03]">
						{#each data.staffMembers as member}
							<tr class="hover:bg-titanium/20 transition-colors">
								<td class="px-6 py-4 text-primary font-medium text-sm">{member.name}</td>
								<td class="px-4 py-4 text-sm text-primary">{roleLabel(member.role)}</td>
								<td class="px-4 py-4 text-sm text-primary">{statusLabel(member.status)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<p class="text-xs text-dim mt-3 text-center">
				{data.staffMembers.length} staff member{data.staffMembers.length !== 1 ? 's' : ''} — Staff can
				log in using their WhatsApp number
			</p>
		{/if}
	</div>
</div>
