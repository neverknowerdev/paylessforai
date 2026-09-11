# Browser E2E tests

These tests run the real PayLessForAI app binary, three deterministic mock provider
processes, and Playwright. They do not use real provider credentials or spend
LLM tokens.

From the repository root:

```sh
cd test/e2e
npm install
npx playwright install chromium
npm test
```

The test server and database assertions use `/tmp/paylessforai-e2e` by default.
For an isolated run, use a fresh directory:

```sh
PAYLESSFORAI_E2E_DATA_DIR=$(mktemp -d) npm test
```

Run only one suite at a time; the app and mock-provider ports are fixed.
