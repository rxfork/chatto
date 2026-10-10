import { expect, test } from './setup';

/** A private forwarding gateway must accept discovery before Chatto sign-in. */
test('discovers the origin server behind a cookie-authenticated proxy', async ({
  page,
  context,
  serverURL
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => {
    // Anonymous viewer and legacy-session probes intentionally return 401.
    if (
      message.type() === 'error' &&
      message.text() !==
        'Failed to load resource: the server responded with a status of 401 (Unauthorized)'
    )
      errors.push(message.text());
  });
  await context.addCookies([
    { name: 'review_proxy', value: 'allowed', url: serverURL, httpOnly: true, sameSite: 'Lax' }
  ]);

  let discoveryRequests = 0;
  await page.route(
    '**/api/connect/chatto.discovery.v1.ServerDiscoveryService/GetServer',
    async (route) => {
      discoveryRequests++;
      const cookie = await route.request().headerValue('cookie');
      if (!cookie?.split(';').some((entry) => entry.trim() === 'review_proxy=allowed')) {
        // Codespaces redirects requests without its forwarding cookie to GitHub.
        await route.fulfill({ status: 302, headers: { location: 'https://github.com/login' } });
        return;
      }
      await route.continue();
    }
  );

  await page.goto('/login');
  await expect(page.getByRole('heading', { name: 'Sign In' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Connect to a server' })).not.toBeVisible();
  expect(discoveryRequests).toBeGreaterThan(0);
  expect(errors).toEqual([]);
});
