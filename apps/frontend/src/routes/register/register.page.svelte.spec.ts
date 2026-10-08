import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import type { PublicServerInfo } from '@chatto/client/api/server';
import RegisterPage from './+page.svelte';

const navigation = vi.hoisted(() => ({ goto: vi.fn() }));

// Page titles are tested separately from this page's partial route/server fixtures.
vi.mock('$lib/render/pageTitle', () => ({ formatPageTitle: () => 'Chatto' }));

vi.mock('$app/navigation', async (importOriginal) => ({
  ...(await importOriginal<typeof import('$app/navigation')>()),
  goto: navigation.goto
}));

function serverInfo(overrides: Partial<PublicServerInfo> = {}): PublicServerInfo {
  return {
    name: 'Invited Community',
    version: '0.5.0',
    authorizeUrl: '/oauth/authorize',
    directRegistrationEnabled: true,
    directLoginEnabled: true,
    accountCreationPolicy: 'invite_only',
    welcomeMessage: null,
    description: null,
    iconUrl: null,
    bannerUrl: null,
    authProviders: [],
    ...overrides
  };
}

describe('invite-only registration', () => {
  beforeEach(() => {
    navigation.goto.mockReset();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('requires an invite link before showing account creation choices', async () => {
    const { getByText, getByLabelText } = render(RegisterPage, {
      props: {
        data: {
          user: null,
          serverInfo: serverInfo(),
          serverInfoLoaded: true,
          inviteAccepted: false,
          inviteError: false
        }
      }
    });

    await expect
      .element(getByText('You need a valid invite link to create an account on this server.'))
      .toBeVisible();
    await expect.element(getByLabelText('Email')).not.toBeInTheDocument();
  });

  it('shows account creation choices after an invite link is accepted', async () => {
    const { getByRole, getByLabelText } = render(RegisterPage, {
      props: {
        data: {
          user: null,
          serverInfoLoaded: true,
          inviteAccepted: true,
          inviteError: false,
          serverInfo: serverInfo({
            directRegistrationEnabled: false,
            authProviders: [
              {
                id: 'company',
                type: 'oidc',
                label: 'Company SSO',
                loginUrl: '/auth/providers/company',
                issuerUrl: 'https://id.example',
                autoProvision: true
              }
            ]
          })
        }
      }
    });

    await expect.element(getByLabelText('Email')).not.toBeInTheDocument();
    await expect.element(getByRole('link', { name: 'Continue with Company SSO' })).toBeVisible();
  });

  it('shows the generic invite-link error after an invalid link redirect', async () => {
    const { getByText } = render(RegisterPage, {
      props: {
        data: {
          user: null,
          serverInfoLoaded: true,
          inviteAccepted: false,
          inviteError: true,
          serverInfo: serverInfo()
        }
      }
    });

    await expect
      .element(getByText('This invite link is invalid or no longer available.'))
      .toBeVisible();
  });

  it('does not offer sign-in-only providers as registration options', async () => {
    const { getByText } = render(RegisterPage, {
      props: {
        data: {
          user: null,
          serverInfoLoaded: true,
          inviteAccepted: false,
          inviteError: false,
          serverInfo: serverInfo({
            directRegistrationEnabled: false,
            authProviders: [
              {
                id: 'sign-in-only',
                type: 'oidc',
                label: 'Sign-in only',
                loginUrl: '/auth/providers/sign-in-only',
                issuerUrl: 'https://id.example',
                autoProvision: false
              }
            ]
          })
        }
      }
    });

    await expect
      .element(getByText('Registration is not available on this instance.'))
      .toBeVisible();
  });
});

describe('email-free registration', () => {
  afterEach(() => vi.unstubAllGlobals());

  it.each(['open', 'invite_only'] as const)(
    'submits username and password without email or a verification token under %s policy',
    async (policy) => {
      const fetchMock = vi
        .fn()
        .mockResolvedValue(new Response(JSON.stringify({ error: 'Try again' }), { status: 400 }));
      vi.stubGlobal('fetch', fetchMock);
      const page = render(RegisterPage, {
        props: {
          data: {
            user: null,
            serverInfo: serverInfo({ accountCreationPolicy: policy, emailDisabled: true }),
            serverInfoLoaded: true,
            inviteAccepted: policy === 'invite_only',
            inviteError: false
          }
        }
      });
      await expect.element(page.getByLabelText('Email')).not.toBeInTheDocument();
      await page.getByLabelText('Username').fill('newuser');
      await page.getByLabelText('Password', { exact: true }).fill('password123');
      await page.getByLabelText('Confirm Password').fill('password123');
      await page.getByRole('button', { name: 'Create Account' }).click();
      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [url, request] = fetchMock.mock.calls[0];
      expect(url).toBe('/auth/browser/register/complete');
      expect(JSON.parse(request.body)).toEqual({
        login: 'newuser',
        password: 'password123',
        passwordConfirmation: 'password123'
      });
    }
  );

  it('requires an accepted invite before showing email-free signup', async () => {
    const page = render(RegisterPage, {
      props: {
        data: {
          user: null,
          serverInfo: serverInfo({ emailDisabled: true }),
          serverInfoLoaded: true,
          inviteAccepted: false,
          inviteError: false
        }
      }
    });
    await expect.element(page.getByLabelText('Username')).not.toBeInTheDocument();
    await expect
      .element(page.getByText('You need a valid invite link to create an account on this server.'))
      .toBeVisible();
  });
});
