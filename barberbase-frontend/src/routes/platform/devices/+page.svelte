<script lang="ts">
	import { enhance } from '$app/forms';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Table from '$lib/components/ui/table';
	import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '$lib/components/ui/card';
	import { Label } from '$lib/components/ui/label';
	import type { PageData, ActionData } from './$types';

	let { data, form }: { data: PageData; form: ActionData } = $props();

	let loading = $state(false);
	let copied = $state(false);

	const STALE_MS = 24 * 60 * 60 * 1000;

	// ponytail: no date lib — Intl + a subtraction covers "relative time" for this admin view.
	function formatLastSeen(iso: string | null): { text: string; stale: boolean } {
		if (!iso) return { text: 'Never seen', stale: true };
		const ms = Date.now() - new Date(iso).getTime();
		const stale = ms > STALE_MS;
		if (ms < 60_000) return { text: 'Just now', stale };
		const mins = Math.floor(ms / 60_000);
		if (mins < 60) return { text: `${mins}m ago`, stale };
		const hrs = Math.floor(mins / 60);
		if (hrs < 48) return { text: `${hrs}h ago`, stale };
		const days = Math.floor(hrs / 24);
		return { text: `${days}d ago`, stale };
	}

	function staffName(staffId: string | null): string {
		if (!staffId) return 'Pooled';
		return data.staff.find((s) => s.id === staffId)?.name ?? 'Pooled';
	}

	function copySecret() {
		if (form?.secret && navigator.clipboard) {
			navigator.clipboard.writeText(form.secret);
			copied = true;
			setTimeout(() => (copied = false), 2000);
		}
	}
</script>

<svelte:head>
	<title>Device Management — BarberBase</title>
</svelte:head>

<div class="min-h-screen bg-canvas text-primary flex flex-col font-manrope">
	<header
		class="bg-matte border-b border-white/[0.03] px-6 py-4 flex justify-between items-center"
	>
		<div class="flex items-center space-x-3">
			<span class="text-xl font-extrabold text-gold-accent tracking-wider">BarberBase</span>
			<span class="text-dim">|</span>
			<span class="text-sm font-semibold text-primary">Device Management</span>
		</div>
		<a href="/platform" class="text-xs font-semibold text-muted hover:text-primary">
			&larr; Operator Console
		</a>
	</header>

	<main class="flex-1 max-w-5xl w-full mx-auto p-6 space-y-6">
		<!-- Location loader -->
		<Card>
			<CardHeader>
				<CardTitle>Location</CardTitle>
				<CardDescription>Paste the tenant and location IDs to manage its hardware devices.</CardDescription>
			</CardHeader>
			<CardContent>
				<form method="GET" class="grid grid-cols-1 md:grid-cols-3 gap-4 items-end">
					<div class="space-y-1.5">
						<Label for="tenant_id">Tenant ID</Label>
						<input
							type="text"
							id="tenant_id"
							name="tenant_id"
							value={data.tenant_id}
							placeholder="uuid"
							class="w-full bg-canvas border border-white/[0.03] rounded-xl px-4 py-2.5 text-primary font-mono text-xs focus:outline-none focus:border-gold-accent"
						/>
					</div>
					<div class="space-y-1.5">
						<Label for="location_id">Location ID</Label>
						<input
							type="text"
							id="location_id"
							name="location_id"
							value={data.location_id}
							placeholder="uuid"
							class="w-full bg-canvas border border-white/[0.03] rounded-xl px-4 py-2.5 text-primary font-mono text-xs focus:outline-none focus:border-gold-accent"
						/>
					</div>
					<Button type="submit">Load Devices</Button>
				</form>
			</CardContent>
		</Card>

		{#if data.loadError}
			<div class="bg-system-error/10 border border-system-error/30 rounded-2xl p-4 text-sm text-system-error">
				{data.loadError}
			</div>
		{/if}

		{#if form?.error}
			<div class="bg-system-error/10 border border-system-error/30 rounded-2xl p-4 text-sm text-system-error">
				{form.error}
			</div>
		{/if}

		{#if form?.deviceCreated}
			<Card class="border-gold-accent/30">
				<CardHeader>
					<CardTitle class="text-gold-accent">Device Created: {form.deviceLabel}</CardTitle>
					<CardDescription class="text-system-error/80 font-semibold">
						This secret will not be shown again — copy it now.
					</CardDescription>
				</CardHeader>
				<CardContent>
					<div class="flex items-center gap-3 bg-canvas border border-white/[0.03] rounded-xl px-4 py-3">
						<code class="text-sm font-mono text-primary select-all break-all flex-1">{form.secret}</code>
						<Button type="button" size="sm" variant="secondary" onclick={copySecret}>
							{copied ? 'Copied' : 'Copy'}
						</Button>
					</div>
				</CardContent>
			</Card>
		{/if}

		{#if data.location_id}
			<!-- Create device -->
			<Card>
				<CardHeader>
					<CardTitle>Add Device</CardTitle>
				</CardHeader>
				<CardContent>
					<form
						method="POST"
						action="?/createDevice"
						use:enhance={() => {
							loading = true;
							return async ({ update }) => {
								await update();
								loading = false;
							};
						}}
						class="flex flex-col md:flex-row gap-4 md:items-end"
					>
						<div class="space-y-1.5 flex-1">
							<Label for="label">Device Label</Label>
							<input
								type="text"
								id="label"
								name="label"
								required
								disabled={loading}
								placeholder="e.g. Chair 1 call-next button"
								class="w-full bg-canvas border border-white/[0.03] rounded-xl px-4 py-2.5 text-primary text-sm focus:outline-none focus:border-gold-accent"
							/>
						</div>
						<Button type="submit" disabled={loading}>
							{loading ? 'Creating...' : 'Create Device'}
						</Button>
					</form>
				</CardContent>
			</Card>

			<!-- Device list -->
			<Card>
				<CardHeader>
					<CardTitle>Devices</CardTitle>
					<CardDescription>{data.devices.length} device(s) at this location.</CardDescription>
				</CardHeader>
				<CardContent>
					{#if data.devices.length === 0}
						<p class="text-sm text-muted">No devices yet.</p>
					{:else}
						<Table.Root>
							<Table.Header>
								<Table.Row>
									<Table.Head>Label</Table.Head>
									<Table.Head>Status</Table.Head>
									<Table.Head>Last Seen</Table.Head>
									<Table.Head>Buttons</Table.Head>
									<Table.Head>Active</Table.Head>
								</Table.Row>
							</Table.Header>
							<Table.Body>
								{#each data.devices as device (device.id)}
									{@const seen = formatLastSeen(device.last_seen_at)}
									<Table.Row>
										<Table.Cell class="font-semibold">{device.label}</Table.Cell>
										<Table.Cell>
											<Badge variant={device.is_active ? 'default' : 'outline'}>
												{device.is_active ? 'Active' : 'Inactive'}
											</Badge>
										</Table.Cell>
										<Table.Cell>
											<span class={seen.stale ? 'text-system-error/80 font-semibold' : 'text-muted'}>
												{seen.text}
												{#if seen.stale}<span class="ml-1 text-[10px] uppercase tracking-wider">Stale</span>{/if}
											</span>
										</Table.Cell>
										<Table.Cell>
											<div class="space-y-2">
												{#each device.buttons as btn (btn.id)}
													<div class="flex items-center gap-2 text-xs">
														<code class="font-mono text-gold-accent bg-titanium px-1.5 py-0.5 rounded">{btn.button_code}</code>
														<span class="text-primary">{btn.label}</span>
														<span class="text-dim">— {staffName(btn.staff_member_id)}</span>
													</div>
												{/each}
												<form
													method="POST"
													action="?/addButton"
													use:enhance
													class="flex flex-wrap gap-2 items-end pt-1"
												>
													<input type="hidden" name="device_id" value={device.id} />
													<input
														type="text"
														name="button_code"
														required
														placeholder="code"
														class="w-20 bg-canvas border border-white/[0.03] rounded-lg px-2 py-1.5 text-primary font-mono text-xs focus:outline-none focus:border-gold-accent"
													/>
													<input
														type="text"
														name="label"
														placeholder="label (optional)"
														class="w-32 bg-canvas border border-white/[0.03] rounded-lg px-2 py-1.5 text-primary text-xs focus:outline-none focus:border-gold-accent"
													/>
													<select
														name="staff_member_id"
														class="bg-canvas border border-white/[0.03] rounded-lg px-2 py-1.5 text-primary text-xs focus:outline-none focus:border-gold-accent"
													>
														<option value="">Pooled (no barber)</option>
														{#each data.staff as staffMember (staffMember.id)}
															<option value={staffMember.id}>{staffMember.name}</option>
														{/each}
													</select>
													<Button type="submit" size="sm" variant="secondary">Add Button</Button>
												</form>
											</div>
										</Table.Cell>
										<Table.Cell>
											<form method="POST" action="?/toggleActive" use:enhance>
												<input type="hidden" name="device_id" value={device.id} />
												<input type="hidden" name="is_active" value={(!device.is_active).toString()} />
												<Button type="submit" size="sm" variant={device.is_active ? 'outline' : 'secondary'}>
													{device.is_active ? 'Disable' : 'Enable'}
												</Button>
											</form>
										</Table.Cell>
									</Table.Row>
								{/each}
							</Table.Body>
						</Table.Root>
					{/if}
				</CardContent>
			</Card>
		{/if}
	</main>
</div>
