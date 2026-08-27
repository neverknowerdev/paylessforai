import { expect, test } from '@playwright/test';

test('browses the populated public catalog and searches normalized aliases', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'PayLessForAI Stat Server' })).toBeVisible();
  await expect(page.locator('#status')).toContainText('models');
  await page.locator('#q').fill('deepseek-v4-pro');
  await page.getByRole('button', { name: 'Search' }).click();
  await expect(page.locator('#rows')).toContainText('DeepSeek V4 Pro');
  await expect(page.locator('#status')).toContainText('models');
});

test('requires admin authentication and renders the private console', async ({ page }) => {
  await page.goto(`${process.env.STAT_SERVER_ADMIN_URL ?? 'http://127.0.0.1:9581'}/`);
  await expect(page.getByRole('heading', { name: 'Stat Server Admin' })).toBeVisible();
  await page.locator('input[name="email"]').fill('admin@localhost');
  await page.locator('input[name="password"]').fill(process.env.STAT_SERVER_E2E_ADMIN_PASSWORD ?? 'e2e-password');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Stat Server Administration' })).toBeVisible();
  await expect(page.locator('#profiles')).toContainText('Programming');
  await expect(page.locator('#sources')).toBeVisible();
  await page.getByRole('textbox', { name: 'Filter models or providers' }).fill('deepseek-v4-pro');
  await page.getByRole('button', { name: 'Search pricing' }).click();
  await expect(page.locator('#pricing')).toContainText('DeepSeek V4 Pro');
  await expect(page.locator('#pricing')).toContainText('OpenRouter');
});
