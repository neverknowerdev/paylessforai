import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  use: { baseURL: process.env.STAT_SERVER_PUBLIC_URL ?? 'http://127.0.0.1:9580', trace: 'retain-on-failure' },
});
