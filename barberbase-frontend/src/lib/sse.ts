import { getApiBase } from './api/client';
import type { QueueStore } from './stores/queue.svelte';

export function connectSSE(
	locationId: string,
	accessToken: string,
	store: QueueStore,
	apiBase?: string,
	platform?: any
) {
	const base = apiBase || getApiBase(platform);
	const url = `${base}/v1/stream/${locationId}?token=${accessToken}`;

	let sse: EventSource | null = null;
	let debounceTimer: any = null;
	let reconnectTimer: any = null;
	let delay = 1000;
	let isClosed = false;

	function triggerDebouncedFetch() {
		if (debounceTimer) {
			clearTimeout(debounceTimer);
		}
		debounceTimer = setTimeout(() => {
			if (!isClosed) {
				store.fetchSnapshot();
			}
		}, 500);
	}

	function handleEvent(dataStr: string) {
		try {
			const data = JSON.parse(dataStr);
			if (data && typeof data.queue_version === 'number') {
				if (data.queue_version > store.localQueueVersion) {
					triggerDebouncedFetch();
				}
			}
		} catch (err) {
			console.error('[SSE] Failed to parse event data:', err);
		}
	}

	function connect() {
		if (isClosed) return;

		// Clean up any existing instance first
		if (sse) {
			sse.close();
		}

		sse = new EventSource(url);

		sse.onopen = () => {
			if (isClosed) {
				sse?.close();
				return;
			}
			store.setSseConnected(true);
			delay = 1000; // Reset exponential backoff delay on successful connection
			store.fetchSnapshot(); // Call fetchSnapshot immediately on connect/reconnect
		};

		sse.onerror = () => {
			if (sse) {
				sse.close();
				sse = null;
			}
			store.setSseConnected(false);

			if (!isClosed) {
				if (reconnectTimer) {
					clearTimeout(reconnectTimer);
				}
				reconnectTimer = setTimeout(() => {
					delay = Math.min(delay * 2, 30000);
					connect();
				}, delay);
			}
		};

		sse.addEventListener('queue_changed', (e) => {
			handleEvent(e.data);
		});

		sse.addEventListener('heartbeat', (e) => {
			handleEvent(e.data);
		});
	}

	connect();

	return {
		close() {
			isClosed = true;
			if (sse) {
				sse.close();
				sse = null;
			}
			if (debounceTimer) {
				clearTimeout(debounceTimer);
			}
			if (reconnectTimer) {
				clearTimeout(reconnectTimer);
			}
			store.setSseConnected(false);
		}
	};
}
