// place files you want to import through the `$lib` alias in this folder.

// "09:00" → "9:00 AM". Tolerates a stray ISO timestamp by grabbing the
// first HH:MM it finds; anything unparseable passes through untouched.
export function formatHHMM(t: string): string {
	const m = /(\d{1,2}):(\d{2})/.exec(t);
	if (!m) return t;
	const h = +m[1];
	return `${((h + 11) % 12) + 1}:${m[2]} ${h >= 12 ? 'PM' : 'AM'}`;
}
