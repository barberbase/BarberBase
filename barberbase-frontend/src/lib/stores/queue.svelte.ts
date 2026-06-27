import { ApiClient, type components } from '$lib/api/client';

export type QueueSnapshot = components['schemas']['QueueSnapshot'];
export type QueueEntryStaff = components['schemas']['QueueEntryStaff'];

export class QueueStore {
	private client: ApiClient;

	// Svelte 5 Runes for reactive state
	snapshot = $state<QueueSnapshot | null>(null);
	localQueueVersion = $state<number>(0);
	sseConnected = $state<boolean>(false);

	constructor(accessToken: string, initialSnapshot: QueueSnapshot | null = null, apiBase?: string) {
		const platform = apiBase ? { env: { PUBLIC_API_BASE: apiBase } } : undefined;
		this.client = new ApiClient(accessToken, platform);
		if (initialSnapshot) {
			this.snapshot = initialSnapshot;
			this.localQueueVersion = initialSnapshot.queue_version;
		}
	}

	async fetchSnapshot() {
		try {
			const res = await this.client.get<QueueSnapshot>('/v1/staff/queue/snapshot');
			this.updateSnapshot(res);
		} catch (err) {
			console.error('[QueueStore] Failed to fetch queue snapshot:', err);
		}
	}

	updateSnapshot(newSnapshot: QueueSnapshot) {
		this.snapshot = newSnapshot;
		this.localQueueVersion = newSnapshot.queue_version;
	}

	setSseConnected(connected: boolean) {
		this.sseConnected = connected;
	}

	// Staff actions
	async callNext() {
		const res = await this.client.post<QueueEntryStaff>('/v1/staff/queue/call-next');
		await this.fetchSnapshot();
		return res;
	}

	async startService(entryId: string) {
		const res = await this.client.post<QueueEntryStaff>(`/v1/staff/queue/entries/${entryId}/start`);
		await this.fetchSnapshot();
		return res;
	}

	async skipEntry(entryId: string) {
		await this.client.post<void>(`/v1/staff/queue/entries/${entryId}/skip`);
		await this.fetchSnapshot();
	}

	async markNoShow(entryId: string) {
		await this.client.post<void>(`/v1/staff/queue/entries/${entryId}/no-show`);
		await this.fetchSnapshot();
	}

	async reactivateEntry(entryId: string) {
		await this.client.post<void>(`/v1/staff/queue/entries/${entryId}/reactivate`);
		await this.fetchSnapshot();
	}

	async confirmArrival(entryId: string) {
		await this.client.post<void>(`/v1/staff/queue/entries/${entryId}/confirm-arrival`);
		await this.fetchSnapshot();
	}

	async reassignBarber(entryId: string, newBarberId: string) {
		await this.client.post<void>(`/v1/staff/queue/entries/${entryId}/reassign`, {
			new_barber_id: newBarberId
		});
		await this.fetchSnapshot();
	}

	async addWalkIn(body: {
		variant_ids: string[];
		customer_name?: string;
		phone_number?: string;
		party_size?: number;
		requested_barber_id?: string;
	}) {
		const res = await this.client.post<QueueEntryStaff>('/v1/staff/queue/add-walkin', {
			...body,
			idempotency_key: crypto.randomUUID()
		});
		await this.fetchSnapshot();
		return res;
	}

	async completeService(entryId: string, body: any) {
		const res = await this.client.post<any>(`/v1/staff/queue/entries/${entryId}/complete`, body);
		await this.fetchSnapshot();
		return res;
	}
}
