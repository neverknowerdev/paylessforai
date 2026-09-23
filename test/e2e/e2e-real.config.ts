import { defineConfig } from '@playwright/test';

// Gated real-provider spot check (NEV-46): the app is started externally by
// .github/workflows/e2e-real.yml on an ephemeral data-dir, so there is no
// webServer here. Request-fixture only — no browsers are launched.
export default defineConfig({
  testDir: '.',
  testMatch: 'e2e-real.spec.ts',
  timeout: 120_000,
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  use: { baseURL: process.env.APP_BASE_URL || 'http://127.0.0.1:9472' },
});
