import { expect, test } from '@playwright/test';

test('new models lead the table and the top-right count opens the catalog', async ({ page }) => {
  const route = (model: string, provider: string, options: Record<string, unknown> = {}) => ({ model, upstream_model: model, provider, name: model, pricing: {}, tags: [], input_modalities: ['text'], output_modalities: ['text'], ...options });
  await page.route('**/api/models', (request) => request.fulfill({ json: {
    data: [
      route('a-existing', 'surplus', { pricing: { input: 900000 }, price_available: true, usage_7d: 2 }),
      route('z-new', 'surplus', { is_new: true, added_at: '2026-09-07T08:30:00Z', free: true, tags: ['reasoning'], usage_7d: 7 }),
      route('z-new', 'openrouter', { is_new: true, added_at: '2026-09-07T08:30:00Z', pricing: { input: 100000 }, price_available: true, discount_percent_bps: 8000, discount_input_percent_bps: 8000, discount_output_percent_bps: 7000, usage_7d: 1 }),
    ],
    updated_at: '2026-09-05T10:00:00Z',
  } }));
  await page.goto('/');
  await expect(page.locator('#topbar-model-count')).toHaveText('2 Models');
  await expect(page.locator('#topbar-new-model-count')).toHaveText('1 new');
  await page.locator('#topbar-models').click();
  await expect(page.locator('[data-view-panel="models"]')).toBeVisible();
  await expect(page.locator('#models-table-body tr').first()).toContainText('z-new');
  await expect(page.locator('[data-view-panel="models"] thead')).not.toContainText('Provider');
  await expect(page.locator('#models-table-body tr').first().locator('.model-provider')).toHaveText('OpenRouter');
  await expect(page.locator('#models-table-body .new-model')).toHaveCount(0);
  await expect(page.locator('#models-table-body .new-model-row')).toHaveCount(2);
  const freeRow = page.locator('#models-table-body tr').filter({ has: page.locator('.price-free') });
  await expect(freeRow).toHaveCount(1);
  await expect(freeRow.locator('.price-free')).toHaveText('FREE');
  await expect(freeRow.locator('.pricing-cell .compact-price-part')).toHaveCount(0);
  await expect(freeRow.locator('.model-provider')).toHaveText('Surplus Intelligence');
  await expect(freeRow.locator('td').nth(0)).not.toContainText('FREE');
  await expect(freeRow.locator('td').nth(1)).not.toContainText('FREE');
  await expect(freeRow.locator('.tag-cell')).toContainText('free');
  await expect(page.locator('[data-view-panel="models"] thead')).toContainText('Pricing');
  await expect(page.locator('[data-view-panel="models"] thead')).toContainText('Discount');
  await expect(page.locator('[data-view-panel="models"] thead')).toContainText('Usage · 7d');
  await expect(page.locator('#catalog-refresh-status')).toContainText('Last scanned');
  await page.locator('[data-sort-key="price"]').click();
  await expect(page.locator('#models-table-body tr').first()).toContainText('z-new');
  await page.locator('[data-sort-key="usage"]').click();
  await expect(page.locator('#models-table-body tr').first()).toContainText('z-new');
  await page.locator('[data-filter-key="usage"]').click();
  await page.locator('[data-usage-used]').click();
  await page.locator('[data-filter-apply]').click();
  await expect(page.locator('#models-table-body tr')).toHaveCount(3);
  await page.locator('#models-clear-filters').click();
  await page.locator('#models-table-body tr').first().locator('[data-quick-model-filter="z-new"]').click();
  await expect(page.locator('#models-table-body tr')).toHaveCount(2);
  await page.locator('#models-clear-filters').click();
  await page.locator('#models-search').fill('a-existing');
  await expect(page.locator('#models-table-body tr')).toHaveCount(1);
  await expect(page.locator('#topbar-model-count')).toHaveText('2 Models');
  await page.locator('#topbar-models').click();
  await expect(page.locator('#models-table-body tr')).toHaveCount(3);
  await page.reload();
  await expect(page.locator('#models-table-body tr').first()).toContainText('z-new');
  await page.screenshot({ path: '../../docs/pr-screenshots/models-catalog-desktop.png', fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 520, height: 900 });
  await page.screenshot({ path: '../../docs/pr-screenshots/models-catalog-mobile.png', fullPage: true, animations: 'disabled' });
});

test('refresh failures are visible and an established catalog has no new badge', async ({ page }) => {
  await page.route('**/api/models', (request) => request.fulfill({ json: {
    data: [{ model: 'existing', provider: 'surplus', pricing: {} }], refresh_error: 'surplus: unavailable',
  } }));
  await page.goto('/#models');
  await expect(page.locator('#topbar-new-model-count')).toBeHidden();
  await expect(page.locator('#catalog-refresh-status')).toContainText('Catalog refresh failed: surplus: unavailable');
  await expect(page.locator('#models-table-body .new-model')).toHaveCount(0);
});
