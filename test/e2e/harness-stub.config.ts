import { defineConfig } from '@playwright/test';

// harness-stub.config.ts — docker-harness mode for harness-stub.spec.ts.
// The harness (mock upstreams + app) is provided externally by
// docker-compose.e2e.yml, so there is no webServer here: the default
// playwright.config.ts webServer would start conflicting local servers on
// the same host ports. Used only by .github/workflows/harness-stub-e2e.yml.
export default defineConfig({
  testDir: '.',
  testMatch: 'harness-stub.spec.ts',
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  use: { baseURL: process.env.APP_BASE_URL || 'http://127.0.0.1:9472', trace: 'retain-on-failure' },
});
