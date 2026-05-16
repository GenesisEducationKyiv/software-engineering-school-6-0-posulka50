import { test, expect } from '@playwright/test';

test.describe('Confirmed view', () => {
  test('shows confirmed view when ?confirmed=1 is in URL', async ({ page }) => {
    await page.goto('/?confirmed=1');

    await expect(page.locator('#confirmed-view')).toBeVisible();
    await expect(page.locator('#subscribe-view')).toBeHidden();
    await expect(page.locator('#my-subs-view')).toBeHidden();

    await expect(page.locator('.confirmed-title')).toContainText("You're subscribed!");
    await expect(page.locator('.confirmed-text')).toContainText('Your email has been confirmed');
  });

  test('back link navigates to subscribe page', async ({ page }) => {
    await page.goto('/?confirmed=1');

    await expect(page.locator('#confirmed-view')).toBeVisible();
    await page.click('a.back-link');

    await expect(page).toHaveURL('/');
    await expect(page.locator('#subscribe-view')).toBeVisible();
  });

  test('without ?confirmed=1 shows subscribe form', async ({ page }) => {
    await page.goto('/');

    await expect(page.locator('#subscribe-view')).toBeVisible();
    await expect(page.locator('#confirmed-view')).toBeHidden();
  });
});
