import { defineConfig } from '@playwright/test';

export default defineConfig({
	webServer: { command: 'npm run build && npm run preview', port: 4173 },
	// every spec binds the same API mock port (9090); parallel files collide
	workers: 1,
	testMatch: '**/*.e2e.{ts,js}'
});
