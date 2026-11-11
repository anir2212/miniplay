import { defineConfig } from '@playwright/test';

// Runs against an already running gateway; scripts/admin_portal_check.sh starts one.
export default defineConfig({
  testDir: './e2e-admin',
  timeout: 120000,
  outputDir: process.env.MINIPLAY_E2E_OUTPUT || 'test-results-admin',
  reporter: [['list']],
  use: { baseURL: process.env.MINIPLAY_GATEWAY_URL || 'http://127.0.0.1:8080', browserName: 'chromium', channel: 'chrome', headless: true },
});
