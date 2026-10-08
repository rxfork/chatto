import { describe, expect, it } from 'vitest';
import { load as forgotPassword } from './forgot-password/+page';
import { load as resetPassword } from './reset-password/+page';
import { load as completeRegistration } from './register/complete/+page';

// Only these PageLoad fields are used; route-event fields belong to SvelteKit.
const emailFreeEvent = {
  parent: async () => ({ serverInfo: { emailDisabled: true } }),
  url: new URL('https://chat.example.test/?token=old-email-token')
};

describe('email-only routes', () => {
  it.each([
    [() => forgotPassword(emailFreeEvent as Parameters<typeof forgotPassword>[0]), '/login'],
    [() => resetPassword(emailFreeEvent as Parameters<typeof resetPassword>[0]), '/login'],
    [
      () => completeRegistration(emailFreeEvent as Parameters<typeof completeRegistration>[0]),
      '/register'
    ]
  ] as const)('redirects email-only flows in email-free mode', async (load, destination) => {
    await expect(load()).rejects.toMatchObject({ status: 302, location: destination });
  });
});
