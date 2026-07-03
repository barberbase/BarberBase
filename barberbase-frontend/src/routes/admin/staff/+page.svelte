<script lang="ts">
	import { enhance } from '$app/forms';
	import Icon from '$lib/components/Icon.svelte';
	import type { PageData, ActionData } from './$types';

	let { data, form }: { data: PageData; form: ActionData } = $props();

	let showAddForm = $state(false);

	// Role tier read via badge color; status via presence-style dot + label.
	const roleMeta = (r: string) =>
		({
			owner: { label: 'Owner', cls: 'border-gold-accent/40 bg-gold-accent/10 text-gold-accent' },
			manager: { label: 'Manager', cls: 'border-white/[0.08] bg-titanium text-primary' },
			barber: { label: 'Barber', cls: 'border-white/[0.08] bg-titanium text-muted' }
		})[r] || { label: r, cls: 'border-white/[0.08] bg-titanium text-muted' };
	const statusMeta = (s: string) =>
		({
			idle: { label: 'Idle', dot: 'bg-system-success', cls: 'text-system-success' },
			cutting: { label: 'Cutting', dot: 'bg-gold-accent', cls: 'text-gold-accent' },
			break: { label: 'Break', dot: 'bg-system-warning', cls: 'text-system-warning' },
			offline: { label: 'Offline', dot: 'bg-dim', cls: 'text-dim' }
		})[s] || { label: s, dot: 'bg-dim', cls: 'text-muted' };
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
				class="bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] text-canvas font-bold px-4 py-2 rounded-xl text-sm transition-all duration-150"
			>
				{showAddForm ? 'Cancel' : '+ Add Staff'}
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
				<Icon name="check" size={14} /> Staff member added successfully
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
						class="bg-gold-accent hover:brightness-110 active:brightness-90 active:scale-[0.98] text-canvas font-bold px-6 py-2 rounded-xl text-sm transition-all duration-150"
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
								class="px-6 py-4 text-left font-mono text-[10px] text-muted font-medium uppercase tracking-wider"
								>Name</th
							>
							<th
								class="px-4 py-4 text-left font-mono text-[10px] text-muted font-medium uppercase tracking-wider"
								>Role</th
							>
							<th
								class="px-4 py-4 text-left font-mono text-[10px] text-muted font-medium uppercase tracking-wider"
								>Status</th
							>
						</tr>
					</thead>
					<tbody class="divide-y divide-white/[0.03]">
						{#each data.staffMembers as member}
							{@const role = roleMeta(member.role)}
							{@const status = statusMeta(member.status)}
							<tr class="hover:bg-titanium/20 transition-colors">
								<td class="px-6 py-4 text-primary font-medium text-sm">{member.name}</td>
								<td class="px-4 py-4 text-sm">
									<span
										class="inline-block font-mono text-[10px] font-medium uppercase tracking-wider border rounded px-2 py-0.5 {role.cls}"
										>{role.label}</span
									>
								</td>
								<td class="px-4 py-4 text-sm">
									<span class="inline-flex items-center gap-1.5 {status.cls}">
										<span class="inline-block w-1.5 h-1.5 rounded-full {status.dot}"></span>
										{status.label}
									</span>
								</td>
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
