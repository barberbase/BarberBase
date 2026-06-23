<script lang="ts">
	import { enhance } from '$app/forms';
	import type { PageData, ActionData } from './$types';

	let { data, form }: { data: PageData; form: ActionData } = $props();

	let pasteJson = $state('');
	let copied = $state(false);

	// Derive state from form results
	let isConnected = $derived(!!(form as any)?.connected);
	let isDisconnected = $derived(!!(form as any)?.disconnected);
	let webhookUrl = $derived((form as any)?.webhook_url ?? '');
	let connectedPhone = $derived((form as any)?.phone_number ?? '');

	const REQUIRED_FIELDS = [
		'bhejna_config_version',
		'phone_number',
		'api_key',
		'webhook_secret',
		'whatsapp_status'
	];

	let jsonValidationError = $derived(() => {
		if (!pasteJson.trim()) return '';
		try {
			const p = JSON.parse(pasteJson);
			const missing = REQUIRED_FIELDS.filter((f) => !(f in p));
			if (missing.length > 0) return `Missing: ${missing.join(', ')}`;
		} catch {
			return 'Invalid JSON';
		}
		return '';
	});

	function copyWebhook() {
		navigator.clipboard.writeText(webhookUrl);
		copied = true;
		setTimeout(() => (copied = false), 2000);
	}
</script>

<svelte:head>
	<title>WhatsApp — Admin — BarberBase</title>
	<meta name="description" content="Connect your shop's own WhatsApp number via Bhejna Mode B" />
</svelte:head>

<div class="min-h-screen bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
	<div class="max-w-2xl mx-auto p-6">
		<!-- Header -->
		<div class="flex items-center gap-3 mb-6">
			<a href="/admin" class="text-muted hover:text-white transition-colors text-sm">← Admin</a>
			<span class="text-dim">/</span>
			<h1 class="text-2xl font-bold text-white">WhatsApp Settings</h1>
		</div>

		{#if form?.error}
			<div class="bg-red-900/30 border border-red-700 rounded-xl p-4 mb-6 text-system-error/80 text-sm">
				{form.error}
			</div>
		{/if}

		<!-- Mode info card -->
		<div class="bg-slate-800 border border-white/[0.05] rounded-2xl p-6 mb-6 shadow-xl">
			<div class="flex items-start justify-between">
				<div>
					{#if isConnected}
						<div class="flex items-center gap-2 mb-2">
							<span class="w-2.5 h-2.5 rounded-full bg-green-500 animate-pulse"></span>
							<span class="text-green-400 font-semibold text-sm">Mode B — Your Own Number</span>
						</div>
						<p class="text-white font-mono text-lg">{connectedPhone}</p>
						<p class="text-muted text-xs mt-1">
							Customers see your shop name as the WhatsApp sender
						</p>
					{:else if isDisconnected}
						<div class="flex items-center gap-2 mb-2">
							<span class="w-2.5 h-2.5 rounded-full bg-slate-500"></span>
							<span class="text-muted font-semibold text-sm"
								>Mode A — Shared BarberBase Number</span
							>
						</div>
						<p class="text-primary text-sm">
							Disconnected. You are now using the shared platform number.
						</p>
					{:else}
						<div class="flex items-center gap-2 mb-2">
							<span class="w-2.5 h-2.5 rounded-full bg-slate-500"></span>
							<span class="text-muted font-semibold text-sm"
								>Mode A — Shared BarberBase Number</span
							>
						</div>
						<p class="text-primary text-sm mt-1">
							All your customers message BarberBase's number. Upgrade to Mode B to use your own
							number.
						</p>
					{/if}
				</div>
			</div>
		</div>

		{#if isConnected}
			<!-- Webhook URL copy box -->
			<div
				id="webhook-url-panel"
				class="bg-green-900/20 border border-green-700 rounded-2xl p-6 mb-6 shadow-xl"
			>
				<p class="text-green-400 font-semibold mb-1 text-sm">
					Step 2: Paste this URL into your Bhejna portal
				</p>
				<p class="text-muted text-xs mb-3">Bhejna portal → Developer Settings → Webhook URL</p>
				<div class="flex gap-2 items-center">
					<input
						id="webhook-url-display"
						readonly
						value={webhookUrl}
						class="flex-1 bg-matte border border-white/[0.05] rounded-lg px-3 py-2 text-green-300 text-xs font-mono focus:outline-none"
					/>
					<button
						id="copy-webhook-btn"
						onclick={copyWebhook}
						class="bg-slate-700 hover:bg-slate-600 text-white text-sm px-4 py-2 rounded-lg transition-colors font-medium whitespace-nowrap"
					>
						{copied ? '✓ Copied!' : 'Copy URL'}
					</button>
				</div>
			</div>

			<!-- Disconnect -->
			<div class="bg-slate-800 border border-white/[0.05] rounded-2xl p-6 shadow-xl">
				<h2 class="text-lg font-bold text-white mb-2">Disconnect Own Number</h2>
				<p class="text-muted text-sm mb-4">
					This will revert your shop to the shared BarberBase number. All credentials will be
					cleared.
				</p>
				<form method="POST" action="?/disconnect" use:enhance>
					<button
						id="disconnect-whatsapp-btn"
						type="submit"
						class="bg-red-700/80 hover:bg-red-700 text-white font-bold px-6 py-2 rounded-xl text-sm transition-all"
						onclick={(e) => {
							if (!confirm('Disconnect your own WhatsApp number?')) e.preventDefault();
						}}
					>
						Disconnect
					</button>
				</form>
			</div>
		{:else}
			<!-- Connect form (Mode A → Mode B) -->
			<div class="bg-slate-800 border border-white/[0.05] rounded-2xl p-6 shadow-xl">
				<h2 class="text-lg font-bold text-white mb-2">Connect Your Own WhatsApp Number</h2>
				<p class="text-muted text-sm mb-4">
					In your Bhejna portal, go to <strong class="text-white">Developer Settings</strong> and
					click <strong class="text-white">Copy BarberBase Integration Config</strong>. Then paste
					the JSON here.
				</p>
				<form id="connect-whatsapp-form" method="POST" action="?/connect" use:enhance>
					<div class="mb-4">
						<label for="config-json-input" class="block text-xs text-muted mb-1"
							>Bhejna Integration Config JSON</label
						>
						<textarea
							id="config-json-input"
							name="config_json"
							bind:value={pasteJson}
							rows="7"
							required
							placeholder={'{\n  "bhejna_config_version": "1",\n  "phone_number": "+91...",\n  "api_key": "nxt_live_...",\n  "webhook_secret": "...",\n  "whatsapp_status": "ACTIVE"\n}'}
							class="w-full bg-slate-700 border border-slate-600 rounded-lg px-3 py-2 text-white text-xs font-mono focus:outline-none focus:ring-2 focus:ring-amber-500 resize-none"
						></textarea>
						{#if jsonValidationError()}
							<p class="text-gold-accent text-xs mt-1">{jsonValidationError()}</p>
						{/if}
					</div>
					<button
						id="submit-connect-whatsapp-btn"
						type="submit"
						class="w-full bg-gold-accent hover:bg-amber-400 text-canvas font-bold py-3 rounded-xl text-sm transition-all"
					>
						Connect WhatsApp
					</button>
				</form>
			</div>
		{/if}
	</div>
</div>
