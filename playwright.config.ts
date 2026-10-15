import { defineConfig, devices } from '@playwright/test';

/**
 * Two E2E targets share one suite:
 *
 * - fixture mode (default, used by CI): the production build on 4173 answered
 *   by contract-bound HTTP doubles;
 * - control-plane mode (`KOWA_E2E_CONTROL_PLANE_URL`): the same journeys
 *   against the real control plane with isolated external adapters, started by
 *   `scripts/web-e2e-control-plane.sh` (it owns the server lifecycle, so the
 *   static preview servers are not started here).
 */
const controlPlaneURL = process.env.KOWA_E2E_CONTROL_PLANE_URL ?? '';
const baseURL = controlPlaneURL === '' ? 'http://127.0.0.1:4173' : controlPlaneURL;

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0,
  // The control-plane harness serves one OAuth stub whose selected identity is
  // a process-wide test fact; parallel journeys would race on it while the
  // fixture double keeps its state per page. CI (fixture mode) stays parallel.
  workers: process.env.CI ? 2 : controlPlaneURL === '' ? undefined : 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    // The local control-plane harness terminates TLS with a runtime-generated
    // self-signed certificate; browser journeys must still be same-origin.
    ignoreHTTPSErrors: true,
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    { name: 'firefox', use: { ...devices['Desktop Firefox'] } },
    { name: 'webkit', use: { ...devices['Desktop Safari'] } },
  ],
  webServer: controlPlaneURL === ''
    ? [
        {
          command: 'pnpm preview --port 4173 --strictPort',
          url: 'http://127.0.0.1:4173',
          reuseExistingServer: false,
        },
        {
          command: 'pnpm test:browser-fixture',
          url: 'http://127.0.0.1:4174',
          reuseExistingServer: false,
        },
      ]
    : undefined,
});
