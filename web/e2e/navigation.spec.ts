import { test, expect } from '@playwright/test';
import { primeActiveSite } from './fixtures/session';

test.describe('hash-based navigation', () => {
  test.beforeEach(async ({ page }) => {
    await primeActiveSite(page, process.env.STATNIVE_E2E_SITE_A!);
    await page.goto('/app/');
    await expect(page.getByTestId('kpi-primary')).toBeVisible({ timeout: 10_000 });
  });

  test('clicking Sources tab updates URL + renders Sources heading', async ({ page }) => {
    await page.getByRole('tab', { name: 'Sources' }).click();
    await expect(page).toHaveURL(/#sources/);
    await expect(page.getByRole('heading', { level: 2, name: 'Sources' })).toBeVisible();
  });

  test('direct-load of #seo renders SEO heading', async ({ page }) => {
    await page.goto('/app/#seo');
    await expect(page.getByRole('heading', { level: 2, name: 'SEO' })).toBeVisible();
  });

  test('back button returns to Overview from Pages', async ({ page }) => {
    await page.getByRole('tab', { name: 'Pages' }).click();
    await expect(page.getByRole('heading', { level: 2, name: 'Pages' })).toBeVisible();
    await page.goBack();
    await expect(page.getByRole('heading', { level: 2, name: 'Overview' })).toBeVisible();
  });

  test('forward button re-renders Pages after back', async ({ page }) => {
    await page.getByRole('tab', { name: 'Pages' }).click();
    await expect(page.getByRole('heading', { level: 2, name: 'Pages' })).toBeVisible();
    await page.goBack();
    await expect(page.getByRole('heading', { level: 2, name: 'Overview' })).toBeVisible();
    await page.goForward();
    await expect(page.getByRole('heading', { level: 2, name: 'Pages' })).toBeVisible();
  });

  test('invalid hash falls through to Overview', async ({ page }) => {
    await page.goto('/app/#garbage');
    await expect(page.getByTestId('kpi-primary')).toBeVisible();
  });
});
