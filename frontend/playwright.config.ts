import { defineConfig, devices } from '@playwright/test'

const e2eBaseURL = 'http://127.0.0.1:4174'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  reporter: 'list',
  use: {
    baseURL: e2eBaseURL,
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: 'npm run dev -- --mode e2e --host 127.0.0.1 --port 4174 --strictPort',
    url: e2eBaseURL,
    reuseExistingServer: false,
    // E2E must never depend on inherited developer or CI environment variables.
    env: {
      VITE_WALLET_API: 'mock',
    },
  },
})
