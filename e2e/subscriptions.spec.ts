import { test, expect, Page } from '@playwright/test';

async function goToSubscriptionsView(page: Page) {
  await page.goto('/');
  await page.click('button.text-link');
  await expect(page.locator('#my-subs-view')).toBeVisible();
}

async function loadSubscriptions(page: Page, email: string) {
  await page.fill('#subs-email', email);
  await page.click('#subs-btn');
}

test.describe('My subscriptions view', () => {
  test('navigate to subscriptions view and back', async ({ page }) => {
    await page.goto('/');

    await page.click('button.text-link');
    await expect(page.locator('#my-subs-view')).toBeVisible();
    await expect(page.locator('#subscribe-view')).toBeHidden();

    await page.locator('#my-subs-view button.text-link').click();
    await expect(page.locator('#subscribe-view')).toBeVisible();
    await expect(page.locator('#my-subs-view')).toBeHidden();
  });

  test('empty result — shows no subscriptions message', async ({ page }) => {
    await page.route('/api/subscriptions*', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }),
    );

    await goToSubscriptionsView(page);
    await loadSubscriptions(page, 'user@example.com');

    await expect(page.locator('#subs-empty')).toBeVisible();
    await expect(page.locator('#subs-empty')).toContainText('No confirmed subscriptions found');
    await expect(page.locator('#subs-list')).toBeHidden();
  });

  test('shows list of confirmed subscriptions', async ({ page }) => {
    const subs = [
      { id: '1', repo_id: 'r1', repo: 'golang/go', email: 'user@example.com', confirmed: true },
      { id: '2', repo_id: 'r2', repo: 'gin-gonic/gin', email: 'user@example.com', confirmed: true },
    ];

    await page.route('/api/subscriptions*', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(subs) }),
    );

    await goToSubscriptionsView(page);
    await loadSubscriptions(page, 'user@example.com');

    await expect(page.locator('#subs-list')).toBeVisible();
    const items = page.locator('#subs-list li');
    await expect(items).toHaveCount(2);
    await expect(items.nth(0)).toContainText('golang/go');
    await expect(items.nth(1)).toContainText('gin-gonic/gin');
  });

  test('confirmed subscription shows Confirmed badge', async ({ page }) => {
    const subs = [
      { id: '1', repo_id: 'r1', repo: 'golang/go', email: 'user@example.com', confirmed: true },
    ];

    await page.route('/api/subscriptions*', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(subs) }),
    );

    await goToSubscriptionsView(page);
    await loadSubscriptions(page, 'user@example.com');

    await expect(page.locator('.badge-confirmed')).toBeVisible();
    await expect(page.locator('.badge-confirmed')).toContainText('Confirmed');
  });

  test('API error — shows error message', async ({ page }) => {
    await page.route('/api/subscriptions*', (route) =>
      route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: 'invalid email format' }) }),
    );

    await goToSubscriptionsView(page);
    await loadSubscriptions(page, 'user@example.com');

    await expect(page.locator('#subs-msg')).toHaveClass(/error/);
    await expect(page.locator('#subs-msg')).toContainText('invalid email format');
    await expect(page.locator('#subs-list')).toBeHidden();
  });

  test('network error — shows generic error message', async ({ page }) => {
    await page.route('/api/subscriptions*', (route) => route.abort());

    await goToSubscriptionsView(page);
    await loadSubscriptions(page, 'user@example.com');

    await expect(page.locator('#subs-msg')).toHaveClass(/error/);
    await expect(page.locator('#subs-msg')).toContainText('Network error');
  });

  test('load button disabled during request', async ({ page }) => {
    await page.route('/api/subscriptions*', async (route) => {
      await new Promise((r) => setTimeout(r, 500));
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' });
    });

    await goToSubscriptionsView(page);
    await page.fill('#subs-email', 'user@example.com');
    const clickPromise = page.click('#subs-btn');

    await expect(page.locator('#subs-btn')).toBeDisabled();
    await expect(page.locator('#subs-btn')).toHaveText('Loading…');

    await clickPromise;
    await expect(page.locator('#subs-btn')).toBeEnabled();
    await expect(page.locator('#subs-btn')).toHaveText('Load');
  });
});
