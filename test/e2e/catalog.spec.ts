import { expect, test } from '@playwright/test';

test('new models lead the table and the top-right count opens the catalog', async ({ page }) => {
  const route = (model: string, provider: string, isNew = false) => ({ model, upstream_model: model, provider, is_new: isNew, pricing: {}, tags: [], input_modalities: ['text'], output_modalities: ['text'] });
  await page.route('**/api/models', (request) => request.fulfill({ json: {
    data: [route('a-existing', 'surplus'), route('z-new', 'surplus', true), route('z-new', 'openrouter', true)],
    updated_at: '2026-09-05T10:00:00Z',
  } }));
  await page.goto('/');
  await expect(page.locator('#topbar-model-count')).toHaveText('2 Models');
  await expect(page.locator('#topbar-new-model-count')).toHaveText('1 new');
  await page.locator('#topbar-models').click();
  await expect(page.locator('[data-view-panel="models"]')).toBeVisible();
  await expect(page.locator('#models-table-body tr').first()).toContainText('z-new');
  await expect(page.locator('#models-table-body tr').first().locator('.new-model')).toHaveText('New');
  await expect(page.locator('#catalog-refresh-status')).toContainText('Last scanned');
  await page.locator('#models-search').fill('a-existing');
  await expect(page.locator('#models-table-body tr')).toHaveCount(1);
  await expect(page.locator('#topbar-model-count')).toHaveText('2 Models');
  await page.locator('#topbar-models').click();
  await expect(page.locator('#models-table-body tr')).toHaveCount(2);
  await page.reload();
  await expect(page.locator('#models-table-body tr').first()).toContainText('z-new');
  await page.screenshot({ path: '/tmp/payless-catalog-new-models.png', fullPage: true, animations: 'disabled' });
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
