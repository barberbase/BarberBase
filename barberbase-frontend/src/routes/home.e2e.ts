import { test, expect } from '@playwright/test';

// Marketing homepage. The motion layer is progressive enhancement over a fully
// server-rendered page, so the invariant worth guarding is that no path ever
// leaves content stranded at opacity 0, and that the queue strip cannot shift
// layout. Both broke during the redesign; neither is visible in a unit test.

const stranded = (page: import('@playwright/test').Page) =>
	page.evaluate(
		() =>
			[...document.querySelectorAll('[data-reveal]')].filter(
				(e) => Number(getComputedStyle(e).opacity) < 0.99
			).length
	);

test.describe('homepage', () => {
	test('server-renders the hero and every FAQ answer without client JS', async ({ browser }) => {
		const ctx = await browser.newContext({ javaScriptEnabled: false });
		const page = await ctx.newPage();
		await page.goto('/');

		await expect(page.getByRole('heading', { level: 1 })).toHaveText('Never miss a customer.');
		await expect(page.locator('.strip-row')).toHaveCount(6);
		// FAQPage JSON-LD is only valid while each answer has a visible DOM node.
		await expect(page.locator('.faq-item')).toHaveCount(6);
		await expect(page.getByText('No account, no password.')).toHaveCount(1);
		expect(await stranded(page)).toBe(0);

		await ctx.close();
	});

	test('reveals every section on a gradual scroll and on an instant jump', async ({ page }) => {
		await page.goto('/');
		const height = await page.evaluate(() => document.body.scrollHeight);
		for (let y = 0; y < height; y += 400) {
			await page.evaluate((v) => window.scrollTo(0, v), y);
			await page.waitForTimeout(100);
		}
		await page.waitForTimeout(800);
		expect(await stranded(page)).toBe(0);

		// A jump past a section reports no intersection change, and a section
		// taller than the viewport never crosses the threshold. Both must still
		// resolve, or the page ships with invisible content.
		await page.reload();
		await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
		await page.waitForTimeout(1000);
		expect(await stranded(page)).toBe(0);
	});

	test('queue strip advances without changing its geometry', async ({ page }) => {
		await page.goto('/');
		const clip = page.locator('.strip-clip');
		const before = await clip.evaluate((e) => e.getBoundingClientRect().height);
		const first = await page.locator('.strip-row').first().innerText();

		await expect
			.poll(() => page.locator('.strip-row').first().innerText(), { timeout: 15000 })
			.not.toBe(first);

		expect(await clip.evaluate((e) => e.getBoundingClientRect().height)).toBe(before);
		// will-change is a compositor hint, not a permanent decoration.
		expect(await page.locator('.strip-list').evaluate((e) => e.style.willChange)).toBe('');
	});

	test('pauses the strip while the tab is hidden', async ({ page }) => {
		await page.goto('/');
		const first = await page.locator('.strip-row').first().innerText();
		await page.evaluate(() => {
			Object.defineProperty(document, 'hidden', { value: true, configurable: true });
			Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true });
			document.dispatchEvent(new Event('visibilitychange'));
		});
		await page.waitForTimeout(10000); // > 2 intervals
		expect(await page.locator('.strip-row').first().innerText()).toBe(first);
	});

	test('reduced motion renders the finished page and stops all motion', async ({ browser }) => {
		const ctx = await browser.newContext({ reducedMotion: 'reduce' });
		const page = await ctx.newPage();
		await page.goto('/');

		const first = await page.locator('.strip-row').first().innerText();
		await page.waitForTimeout(6000);
		expect(await page.locator('.strip-row').first().innerText()).toBe(first);
		expect(await stranded(page)).toBe(0);
		expect(
			await page.evaluate(
				() => getComputedStyle(document.querySelector('.pole')!, '::before').animationName
			)
		).toBe('none');

		await ctx.close();
	});

	test('fits a 375px phone with reachable tap targets', async ({ browser }) => {
		const ctx = await browser.newContext({ viewport: { width: 375, height: 667 }, isMobile: true });
		const page = await ctx.newPage();
		await page.goto('/');

		expect(
			await page.evaluate(
				() => document.documentElement.scrollWidth - document.documentElement.clientWidth
			)
		).toBeLessThanOrEqual(0);

		const small = await page.evaluate(() =>
			[...document.querySelectorAll('main a, main button')]
				.map((e) => ({ t: (e as HTMLElement).innerText.trim(), h: e.getBoundingClientRect().height }))
				.filter((x) => x.h > 0 && x.h < 44)
		);
		expect(small).toEqual([]);

		// Both hero CTAs must be reachable without scrolling.
		const bottom = await page.locator('main a').nth(1).evaluate((e) => e.getBoundingClientRect().bottom);
		expect(bottom).toBeLessThanOrEqual(667);

		await ctx.close();
	});
});
