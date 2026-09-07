import { defineConfig, devices } from '@playwright/test'

const startDevServer = process.env.ASTRORDER_E2E_START_DEV_SERVER === '1'
const executablePath = process.env.ASTRORDER_E2E_EXECUTABLE
const defaultBaseUrl = startDevServer ? 'http://127.0.0.1:5173' : 'http://127.0.0.1:8765'

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  reporter: process.env.CI ? 'line' : 'list',
  use: {
    baseURL: process.env.ASTRORDER_E2E_BASE_URL || defaultBaseUrl,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    ...(executablePath ? { launchOptions: { executablePath } } : {}),
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], browserName: 'chromium' } },
    { name: 'mobile', use: { ...devices['iPhone 13'], browserName: 'chromium' } },
  ],
  webServer: startDevServer
    ? {
        command: 'npm run dev -- --host 127.0.0.1',
        url: 'http://127.0.0.1:5173',
        reuseExistingServer: !process.env.CI,
        timeout: 30_000,
      }
    : undefined,
})
