import { test, expect, Page } from '@playwright/test';

async function fillAndSubmit(page: Page, repo: string, email: string) {
  await page.fill('#repo', repo);
  await page.fill('#email', email);
  await page.click('#btn');
}

async function mockSubscribe(page: Page, status: number, body: object) {
  await page.route('/api/subscribe', (route) =>
    route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) }),
  );
}

test.describe('Subscribe page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
  });

  test('loads with form visible', async ({ page }) => {
    await expect(page.locator('#subscribe-view h1')).toContainText('Release Notifier');
    await expect(page.locator('#repo')).toBeVisible();
    await expect(page.locator('#email')).toBeVisible();
    await expect(page.locator('#btn')).toBeVisible();
    await expect(page.locator('#btn')).toBeEnabled();
  });

  test('success — shows confirmation message', async ({ page }) => {
    await mockSubscribe(page, 200, { message: 'Subscription successful. Confirmation email sent.' });

    await fillAndSubmit(page, 'golang/go', 'user@example.com');

    await expect(page.locator('#msg')).toHaveClass(/success/);
    await expect(page.locator('#msg')).toContainText('Check your inbox');
  });

  test('success — resets form fields after submit', async ({ page }) => {
    await mockSubscribe(page, 200, { message: 'Subscription successful. Confirmation email sent.' });

    await fillAndSubmit(page, 'golang/go', 'user@example.com');

    await expect(page.locator('#msg')).toHaveClass(/success/);
    await expect(page.locator('#repo')).toHaveValue('');
    await expect(page.locator('#email')).toHaveValue('');
  });

  test('400 — shows error from API', async ({ page }) => {
    // Use a browser-valid email so native validation doesn't block submit;
    // the server-side error is simulated via the route mock.
    await mockSubscribe(page, 400, { error: 'invalid email format' });

    await fillAndSubmit(page, 'golang/go', 'user@example.com');

    await expect(page.locator('#msg')).toHaveClass(/error/);
    await expect(page.locator('#msg')).toContainText('invalid email format');
  });

  test('404 — repo not found shows error', async ({ page }) => {
    await mockSubscribe(page, 404, { error: 'repository not found on GitHub' });

    await fillAndSubmit(page, 'nobody/norepo', 'user@example.com');

    await expect(page.locator('#msg')).toHaveClass(/error/);
    await expect(page.locator('#msg')).toContainText('repository not found');
  });

  test('409 — duplicate subscription shows error', async ({ page }) => {
    await mockSubscribe(page, 409, { error: 'email already subscribed to this repository' });

    await fillAndSubmit(page, 'golang/go', 'user@example.com');

    await expect(page.locator('#msg')).toHaveClass(/error/);
    await expect(page.locator('#msg')).toContainText('already subscribed');
  });

  test('429 — rate limit shows error', async ({ page }) => {
    await mockSubscribe(page, 429, { error: 'GitHub API rate limit exceeded, try again later' });

    await fillAndSubmit(page, 'golang/go', 'user@example.com');

    await expect(page.locator('#msg')).toHaveClass(/error/);
    await expect(page.locator('#msg')).toContainText('rate limit');
  });

  test('network error — shows generic error message', async ({ page }) => {
    await page.route('/api/subscribe', (route) => route.abort());

    await fillAndSubmit(page, 'golang/go', 'user@example.com');

    await expect(page.locator('#msg')).toHaveClass(/error/);
    await expect(page.locator('#msg')).toContainText('Network error');
  });

  test('button is disabled while request is in flight', async ({ page }) => {
    // Delay the response so we can observe the disabled state
    await page.route('/api/subscribe', async (route) => {
      await new Promise((r) => setTimeout(r, 500));
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ message: 'ok' }) });
    });

    await page.fill('#repo', 'golang/go');
    await page.fill('#email', 'user@example.com');
    const clickPromise = page.click('#btn');

    await expect(page.locator('#btn')).toBeDisabled();
    await expect(page.locator('#btn')).toHaveText('Subscribing…');

    await clickPromise;
    await expect(page.locator('#btn')).toBeEnabled();
    await expect(page.locator('#btn')).toHaveText('Subscribe');
  });
});
