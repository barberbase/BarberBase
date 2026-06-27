<script lang="ts">
	import { onMount } from 'svelte';
	import { PUBLIC_VAPID_PUBLIC_KEY } from '$env/static/public';

	// Svelte 5 Rune props
	let { jwt } = $props<{ jwt: string }>();

	let showPrompt = $state(false);

	onMount(() => {
		if (!('serviceWorker' in navigator) || !('PushManager' in window)) return;

		let count: number;
		if (!sessionStorage.getItem('bb_dash_session_marked')) {
			sessionStorage.setItem('bb_dash_session_marked', '1');
			count = parseInt(localStorage.getItem('bb_dash_session_count') ?? '0') + 1;
			localStorage.setItem('bb_dash_session_count', String(count));
		} else {
			count = parseInt(localStorage.getItem('bb_dash_session_count') ?? '0');
		}

		if (count >= 2 && Notification.permission === 'default') {
			showPrompt = true;
		}
	});

	function urlBase64ToUint8Array(b64: string): Uint8Array {
		const padding = '='.repeat((4 - (b64.length % 4)) % 4);
		const base64 = (b64 + padding).replace(/-/g, '+').replace(/_/g, '/');
		const raw = atob(base64);
		return Uint8Array.from(raw, (c) => c.charCodeAt(0));
	}

	function arrayBufferToBase64Url(buf: ArrayBuffer | null): string {
		if (!buf) return '';
		return btoa(String.fromCharCode(...new Uint8Array(buf)))
			.replace(/\+/g, '-')
			.replace(/\//g, '_')
			.replace(/=+$/, '');
	}

	async function enablePush() {
		try {
			const reg = await navigator.serviceWorker.register('/service-worker.js', { scope: '/dashboard/' });
			await navigator.serviceWorker.ready;

			const permission = await Notification.requestPermission();
			if (permission !== 'granted') {
				showPrompt = false; // denied — silent SSE-only fallback (Law 21)
				return;
			}

			const sub = await reg.pushManager.subscribe({
				userVisibleOnly: true,
				applicationServerKey: urlBase64ToUint8Array(PUBLIC_VAPID_PUBLIC_KEY)
			});

			const body = {
				endpoint: sub.endpoint,
				p256dh: arrayBufferToBase64Url(sub.getKey('p256dh')),
				auth: arrayBufferToBase64Url(sub.getKey('auth'))
			};

			// Non-2xx swallowed — dashboard works via SSE regardless (Law 21)
			await fetch('https://api.barberbase.in/v1/staff/push/subscribe', {
				method: 'POST',
				headers: {
					'Content-Type': 'application/json',
					Authorization: 'Bearer ' + jwt
				},
				body: JSON.stringify(body)
			}).catch(() => {
				/* swallow */
			});
		} catch (err) {
			// Any failure (SW registration, subscribe, fetch) is swallowed (Law 21)
		} finally {
			showPrompt = false;
		}
	}

	function dismiss() {
		showPrompt = false;
		// No error. No retry prompt. Silent SSE-only mode (Law 21).
	}
</script>

{#if showPrompt}
	<div class="push-prompt-container" id="push-permission-prompt">
		<div class="push-prompt-content">
			<div class="push-prompt-icon">
				<svg
					viewBox="0 0 24 24"
					width="20"
					height="20"
					fill="none"
					stroke="currentColor"
					stroke-width="2"
					stroke-linecap="round"
					stroke-linejoin="round"
				>
					<path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"></path>
					<path d="M13.73 21a2 2 0 0 1-3.46 0"></path>
				</svg>
			</div>
			<div class="push-prompt-text">
				<span class="push-prompt-label"
					>Get notified on your lock screen when a customer is ready.</span
				>
			</div>
			<div class="push-prompt-actions">
				<button class="push-btn-primary" onclick={enablePush} id="btn-enable-notifications">
					Enable Notifications
				</button>
				<button class="push-btn-secondary" onclick={dismiss} id="btn-dismiss-notifications">
					Not Now
				</button>
			</div>
		</div>
	</div>
{/if}

<style>
	.push-prompt-container {
		position: fixed;
		bottom: 24px;
		left: 50%;
		transform: translateX(-50%);
		width: calc(100% - 48px);
		max-width: 500px;
		background: rgba(26, 26, 26, 0.95);
		backdrop-filter: blur(12px);
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 16px;
		box-shadow: 0 10px 30px rgba(0, 0, 0, 0.5);
		z-index: 10000;
		padding: 16px;
		animation: slideUp 0.3s cubic-bezier(0.16, 1, 0.3, 1);
	}

	@keyframes slideUp {
		from {
			transform: translate(-50%, 100%);
			opacity: 0;
		}
		to {
			transform: translate(-50%, 0);
			opacity: 1;
		}
	}

	.push-prompt-content {
		display: flex;
		align-items: center;
		gap: 16px;
	}

	@media (max-width: 600px) {
		.push-prompt-content {
			flex-direction: column;
			align-items: stretch;
			gap: 12px;
		}
	}

	.push-prompt-icon {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 36px;
		height: 36px;
		background: rgba(255, 255, 255, 0.1);
		border-radius: 50%;
		color: #ffffff;
		flex-shrink: 0;
	}

	.push-prompt-text {
		flex-grow: 1;
	}

	.push-prompt-label {
		font-family:
			'Inter',
			system-ui,
			-apple-system,
			sans-serif;
		font-size: 14px;
		font-weight: 500;
		color: #e0e0e0;
		line-height: 1.4;
	}

	.push-prompt-actions {
		display: flex;
		align-items: center;
		gap: 12px;
	}

	@media (max-width: 600px) {
		.push-prompt-actions {
			justify-content: flex-end;
		}
	}

	.push-btn-primary {
		font-family:
			'Inter',
			system-ui,
			-apple-system,
			sans-serif;
		font-size: 13px;
		font-weight: 600;
		color: #1a1a1a;
		background: #ffffff;
		border: none;
		border-radius: 8px;
		padding: 8px 16px;
		cursor: pointer;
		transition:
			background 0.2s ease,
			transform 0.1s ease;
		white-space: nowrap;
	}

	.push-btn-primary:hover {
		background: #e6e6e6;
	}

	.push-btn-primary:active {
		transform: scale(0.97);
	}

	.push-btn-secondary {
		font-family:
			'Inter',
			system-ui,
			-apple-system,
			sans-serif;
		font-size: 13px;
		font-weight: 500;
		color: #a0a0a0;
		background: transparent;
		border: none;
		padding: 8px 12px;
		cursor: pointer;
		transition: color 0.2s ease;
		white-space: nowrap;
		text-decoration: none;
	}

	.push-btn-secondary:hover {
		color: #ffffff;
	}
</style>
