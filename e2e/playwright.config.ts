import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  testMatch: '**/*.spec.ts',
  timeout: 30_000,
  retries: process.env.CI ? 2 : 0,
  use: {
    baseURL: process.env.BASE_URL ?? 'http://localhost:8080',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  // When BASE_URL is provided (CI with docker-compose) no local server is needed.
  // Otherwise serve the static UI on port 8080 and mock all /api/* calls.
  ...(process.env.BASE_URL
    ? {}
    : {
        webServer: {
          command: 'npx serve ../cmd/server/static -p 8080',
          url: 'http://localhost:8080',
          reuseExistingServer: true,
          timeout: 30_000,
        },
      }),
});
