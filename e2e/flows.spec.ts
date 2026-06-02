import { test, expect } from '@playwright/test';
import { clearEmails, waitForEmail, extractConfirmUrl } from './helpers/api';

test.describe('Full E2E flows', () => {
  test.beforeEach(async () => {
    await clearEmails();
  });

  test('subscribe -> confirm -> subscriptions list shows confirmed', async ({ page }) => {
    const email = `e2e-${Date.now()}@example.com`;
    const repo = 'actions/checkout';

    // Step 1: subscribe via UI (hits real backend)
    await page.goto('/');
    await page.fill('#repo', repo);
    await page.fill('#email', email);
    await page.click('#btn');

    await expect(page.locator('#msg')).toHaveClass(/success/);
    await expect(page.locator('#msg')).toContainText('Check your inbox');

    // Step 2: read confirmation email from fake-resend, extract confirm URL
    const sentEmail = await waitForEmail(email);
    const confirmUrl = extractConfirmUrl(sentEmail.html);

    // Step 3: confirm subscription (real backend call)
    await page.goto(confirmUrl);

    // Step 4: navigate to subscriptions view, verify subscription is confirmed
    await page.goto('/');
    await page.click('button.text-link');
    await expect(page.locator('#my-subs-view')).toBeVisible();

    await page.fill('#subs-email', email);
    await page.click('#subs-btn');

    await expect(page.locator('#subs-list')).toBeVisible();
    const items = page.locator('#subs-list li');
    await expect(items).toHaveCount(1);
    await expect(items.first()).toContainText(repo);
    await expect(page.locator('.badge-confirmed')).toBeVisible();
  });

  test('duplicate subscription returns 409 error', async ({ page }) => {
    const email = `e2e-dup-${Date.now()}@example.com`;
    const repo = 'actions/checkout';

    await page.goto('/');

    // First subscription
    await page.fill('#repo', repo);
    await page.fill('#email', email);
    await page.click('#btn');
    await expect(page.locator('#msg')).toHaveClass(/success/);

    // Second subscription — same email + repo
    await page.fill('#repo', repo);
    await page.fill('#email', email);
    await page.click('#btn');

    await expect(page.locator('#msg')).toHaveClass(/error/);
    await expect(page.locator('#msg')).toContainText('already subscribed');
  });

  test('non-existent repo returns 404 error', async ({ page }) => {
    await page.goto('/');
    await page.fill('#repo', 'this-user-xyz/repo-does-not-exist-e2e');
    await page.fill('#email', `e2e-404-${Date.now()}@example.com`);
    await page.click('#btn');

    await expect(page.locator('#msg')).toHaveClass(/error/);
    await expect(page.locator('#msg')).toContainText('not found');
  });
});
