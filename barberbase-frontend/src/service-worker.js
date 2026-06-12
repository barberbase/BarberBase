// Pure browser Service Worker APIs only.
// No SvelteKit imports, no import.meta.env, no MediaSession API, no audio playback.

self.addEventListener('push', (event) => {
	if (!event.data) return;

	let data;
	try {
		data = event.data.json();
	} catch {
		return;
	}

	const title = 'BarberBase — ' + (data.location_name || data.location_id || '');
	const waitingCount =
		typeof data.waiting_count !== 'undefined'
			? data.waiting_count
			: data.waiting_arrived_count || 0;

	event.waitUntil(
		self.registration.showNotification(title, {
			body: waitingCount + ' arrived · NEXT CLIENT ready',
			tag: 'barberbase-queue', // single shared tag; replaces previous notification inline
			silent: true, // Law: no audio, no vibration override, no ring
			requireInteraction: true, // Android: persists on lock screen until dismissed
			actions: [
				{ action: 'call_next', title: 'NEXT CLIENT' },
				{ action: 'open_dashboard', title: 'Open Dashboard' }
			],
			data: {
				pat: data.pat,
				api_url: data.api_url
			}
		})
	);
});

self.addEventListener('notificationclick', (event) => {
	const notification = event.notification;

	if (event.action === 'call_next') {
		notification.close();

		// Construct the endpoint URL safely based on api_url content
		let targetUrl = notification.data.api_url;
		if (!targetUrl.endsWith('/staff/push/call-next')) {
			if (targetUrl.endsWith('/')) {
				targetUrl += 'staff/push/call-next';
			} else {
				targetUrl += '/staff/push/call-next';
			}
		}

		event.waitUntil(
			fetch(targetUrl, {
				method: 'POST',
				headers: { 'X-Push-Action-Token': notification.data.pat }
			})
				.then(async (res) => {
					if (res.status === 429) {
						// Double-tap. Rate limit is 3 s. Do NOT touch the notification.
						return;
					}

					const base = { tag: 'barberbase-queue', silent: true };

					if (res.ok) {
						const body = await res.json();
						const more = body.waiting_arrived_count > 0;
						return self.registration.showNotification('BarberBase', {
							...base,
							body: '✓ Called · ' + body.waiting_arrived_count + ' remaining',
							requireInteraction: more,
							actions: more
								? [
										{ action: 'call_next', title: 'NEXT CLIENT' },
										{ action: 'open_dashboard', title: 'Open Dashboard' }
									]
								: [{ action: 'open_dashboard', title: 'Open Dashboard' }],
							data: notification.data
						});
					}
					if (res.status === 404) {
						return self.registration.showNotification('BarberBase', {
							...base,
							requireInteraction: false,
							body: 'Queue clear · No arrived customers'
						});
					}
					if (res.status === 401 || res.status === 403) {
						return self.registration.showNotification('BarberBase', {
							...base,
							requireInteraction: false,
							body: 'Session expired · Open dashboard to continue'
						});
					}
					// 5xx or unexpected
					return self.registration.showNotification('BarberBase', {
						...base,
						requireInteraction: true,
						body: 'Could not advance queue · Tap to retry',
						actions: [
							{ action: 'call_next', title: 'NEXT CLIENT' },
							{ action: 'open_dashboard', title: 'Open Dashboard' }
						],
						data: notification.data
					});
				})
				.catch(() => {
					return self.registration.showNotification('BarberBase', {
						tag: 'barberbase-queue',
						silent: true,
						requireInteraction: true,
						body: 'Network error · Tap to retry',
						actions: [{ action: 'call_next', title: 'NEXT CLIENT' }],
						data: notification.data
					});
				})
		);
	} else {
		// 'open_dashboard' or bare notification tap
		notification.close();
		event.waitUntil(
			clients.matchAll({ type: 'window', includeUncontrolled: true }).then((list) => {
				for (const c of list) {
					if (c.url.includes('/dashboard') && 'focus' in c) return c.focus();
				}
				return clients.openWindow('/dashboard');
			})
		);
	}
});
