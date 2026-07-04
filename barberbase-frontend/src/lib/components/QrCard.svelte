<script lang="ts">
	import qrcode from 'qrcode-generator';

	let { url, filename = 'barberbase-qr' } = $props<{ url: string; filename?: string }>();

	let canvasEl = $state<HTMLCanvasElement | null>(null);

	// QR spec requires a 4-module quiet zone; white background regardless of theme
	// so it scans and prints reliably.
	function drawQr(canvas: HTMLCanvasElement, data: string, targetSize: number) {
		const qr = qrcode(0, 'M');
		qr.addData(data);
		qr.make();
		const count = qr.getModuleCount();
		const scale = Math.max(1, Math.floor(targetSize / (count + 8)));
		const dim = scale * (count + 8);
		canvas.width = dim;
		canvas.height = dim;
		const ctx = canvas.getContext('2d');
		if (!ctx) return;
		ctx.fillStyle = '#FFFFFF';
		ctx.fillRect(0, 0, dim, dim);
		ctx.fillStyle = '#080808';
		for (let r = 0; r < count; r++) {
			for (let c = 0; c < count; c++) {
				if (qr.isDark(r, c)) {
					ctx.fillRect((c + 4) * scale, (r + 4) * scale, scale, scale);
				}
			}
		}
	}

	$effect(() => {
		if (url && canvasEl) drawQr(canvasEl, url, 320);
	});

	function downloadPng() {
		if (!url) return;
		const offscreen = document.createElement('canvas');
		drawQr(offscreen, url, 1024);
		const a = document.createElement('a');
		a.href = offscreen.toDataURL('image/png');
		a.download = `${filename}.png`;
		a.click();
	}
</script>

{#if url}
	<div class="mt-6 bg-matte border border-white/[0.05] rounded-xl p-5 machined-edge">
		<div class="flex flex-col sm:flex-row items-center sm:items-start gap-5">
			<div class="bg-white rounded-lg p-2 shrink-0">
				<canvas bind:this={canvasEl} class="block w-40 h-40" aria-label="QR code for {url}"
				></canvas>
			</div>
			<div class="min-w-0 text-center sm:text-left">
				<h2 class="text-sm font-bold text-primary mb-1">Shop QR Code</h2>
				<p class="text-xs text-muted mb-3">
					Print this for your counter or standee. Customers scan it to open your shop page and
					join the queue.
				</p>
				<button
					type="button"
					onclick={downloadPng}
					class="px-3 py-1.5 text-xs font-bold rounded-lg border border-gold-accent/30 text-gold-accent hover:bg-gold-accent/10 transition-colors"
				>
					Download PNG
				</button>
			</div>
		</div>
	</div>
{/if}
