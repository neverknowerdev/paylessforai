import { defineConfig } from '@playwright/test';

// matrix.live.config.ts — MANUAL-ONLY live runner (NEV-59 review fix).
// Runs the 27-cell format x provider matrix against REAL providers and REAL
// harnesses. NEVER referenced by any .github/workflows file — CI must spend
// nothing. The operator starts infra by hand and runs:
//
//   docker compose -f docker-compose.e2e-real.yml up -d --build   # real-upstream app host
//   cd test/e2e && OPENROUTER_API_KEY=... npm run matrix:live
//
// With RECORD=1 the app routes through the mockrecord capturing proxy
// (see cmd/mockrecord) and every upstream response is auto-saved as a mock
// fixture under mocks/matrix/recorded/ for later curation into mocks/matrix/.
const record = process.env.RECORD === '1';
const outDir = process.env.RECORD_OUT_DIR || 'mocks/matrix/recorded';

export default defineConfig({
  testDir: '.',
  testMatch: 'matrix.live.spec.ts',
  timeout: 120_000,
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  use: { baseURL: process.env.APP_BASE_URL || 'http://127.0.0.1:9472' },
  webServer: record ? [
    { command: `go run ../../cmd/mockrecord -listen 127.0.0.1:19574 -target https://openrouter.ai/api/v1 -strip-prefix /openrouter/api/v1 -out-dir ${outDir} -prefix openrouter`, url: 'http://127.0.0.1:19574/healthz', reuseExistingServer: true, timeout: 120_000 },
    { command: `go run ../../cmd/mockrecord -listen 127.0.0.1:19575 -target https://api.surplusintelligence.ai/v1 -strip-prefix /surplus/v1 -out-dir ${outDir} -prefix surplus`, url: 'http://127.0.0.1:19575/healthz', reuseExistingServer: true, timeout: 120_000 },
    { command: `go run ../../cmd/mockrecord -listen 127.0.0.1:19576 -target https://api.opencode.ai/zen/go/v1 -strip-prefix /zen/go/v1 -out-dir ${outDir} -prefix opencode`, url: 'http://127.0.0.1:19576/healthz', reuseExistingServer: true, timeout: 120_000 },
  ] : [],
});
