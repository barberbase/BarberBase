const encoder = new TextEncoder();

export function constantTimeEqual(a: string, b: string): boolean {
	if (a.length !== b.length) {
		return false;
	}
	let result = 0;
	for (let i = 0; i < a.length; i++) {
		result |= a.charCodeAt(i) ^ b.charCodeAt(i);
	}
	return result === 0;
}

function uint8ArrayToBase64url(arr: Uint8Array): string {
	let binary = '';
	const len = arr.byteLength;
	for (let i = 0; i < len; i++) {
		binary += String.fromCharCode(arr[i]);
	}
	return btoa(binary).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
}

async function getHmacKey(secret: string): Promise<CryptoKey> {
	const keyData = encoder.encode(secret);
	return crypto.subtle.importKey('raw', keyData, { name: 'HMAC', hash: 'SHA-256' }, false, [
		'sign',
		'verify'
	]);
}

export async function signSession(secret: string, ttlSeconds: number): Promise<string> {
	const exp = Math.floor(Date.now() / 1000) + ttlSeconds;
	const payload = btoa(String(exp)).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_'); // base64url

	const key = await getHmacKey(secret);
	const sigBuffer = await crypto.subtle.sign('HMAC', key, encoder.encode(payload));

	const sigArray = new Uint8Array(sigBuffer);
	const sig = uint8ArrayToBase64url(sigArray);

	return `${payload}.${sig}`;
}

export async function verifySession(token: string, secret: string): Promise<boolean> {
	const parts = token.split('.');
	if (parts.length !== 2) return false;
	const [payload, sig] = parts;

	const key = await getHmacKey(secret);

	// Recompute signature
	const expectedBuffer = await crypto.subtle.sign('HMAC', key, encoder.encode(payload));
	const expectedArray = new Uint8Array(expectedBuffer);
	const expectedSig = uint8ArrayToBase64url(expectedArray);

	if (!constantTimeEqual(sig, expectedSig)) {
		return false;
	}

	// Decode payload (exp timestamp)
	try {
		const decodedPayload = atob(payload.replace(/-/g, '+').replace(/_/g, '/'));
		const exp = parseInt(decodedPayload, 10);
		if (isNaN(exp)) return false;
		return exp > Math.floor(Date.now() / 1000);
	} catch {
		return false;
	}
}
