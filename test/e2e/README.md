# Browser E2E tests

These tests run the real PayLessForAI app binary, two deterministic mock provider
processes, and Playwright. They do not use real provider credentials or spend
LLM tokens.

From the repository root:

```sh
cd test/e2e
npm install
npx playwright install chromium
npm test
```

The test server uses `/tmp/paylessforai-e2e`. Remove that directory between
runs when testing first-run behavior.
