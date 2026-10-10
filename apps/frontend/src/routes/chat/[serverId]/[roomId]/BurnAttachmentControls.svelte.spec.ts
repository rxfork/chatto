import '../../../../app.css';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { Timestamp } from '@bufbuild/protobuf';
import { Asset } from '@chatto/api-types/api/v1/attachments_pb';
import { BurnAttachmentViewerStatus } from '@chatto/api-types/api/v1/message_types_pb';
import { AssetService } from '@chatto/api-types/api/v1/attachments_connect';
import { UserService } from '@chatto/api-types/api/v1/user_service_connect';
import { getUserStore } from '@chatto/client/server/users';
import { mockService, receivedRequest } from '@chatto/client/testing/fakeServer';
import { attachmentView } from '@chatto/client/api/roomTimeline';
import { createTestServerScope } from '$lib/test-utils/serverScope.svelte';
import { getToasts, toast } from '$lib/ui/toast';
import BurnAttachmentControls from './BurnAttachmentControls.svelte';

vi.mock(
  '$lib/state/server/scope.svelte',
  async () => (await import('$lib/test-utils/serverScope.svelte')).serverScopeModule
);

const assets = mockService(AssetService);
function original(confirm = false) {
  return new Asset({
    id: 'secret',
    filename: 'secret.png',
    contentType: 'image/png',
    burn: {
      viewerStatus: BurnAttachmentViewerStatus.BURNED,
      canMakePermanent: true,
      requiresPermanenceConfirmation: confirm,
      unopenedExpiresAt: Timestamp.fromDate(new Date('2099-01-01T00:00:00Z'))
    }
  });
}
function mount(asset = original()) {
  const onchange = vi.fn();
  return {
    ...render(BurnAttachmentControls, {
      attachment: attachmentView(asset),
      roomId: 'room',
      onchange
    }),
    onchange
  };
}
describe('attachment permanence controls', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    toast.clear();
    createTestServerScope({ routes: (router) => router.service(AssetService, assets) });
    assets.getAsset.mockReturnValue({ asset: original() });
    assets.makeAttachmentPermanent.mockReturnValue({
      asset: new Asset({
        id: 'secret',
        burn: { viewerStatus: BurnAttachmentViewerStatus.PERMANENT }
      }),
      undoToken: 'undo',
      undoExpiresAt: Timestamp.fromDate(new Date(Date.now() + 60_000))
    });
    assets.undoAttachmentPermanence.mockReturnValue({ asset: original() });
  });
  it('makes permanent with one click after acknowledgement and offers Undo without resetting the used session', async () => {
    const view = mount();
    await view.getByRole('button', { name: 'Make permanent', exact: true }).click();
    await vi.waitFor(() => expect(assets.makeAttachmentPermanent).toHaveBeenCalled());
    expect(receivedRequest(assets.makeAttachmentPermanent)).toMatchObject({
      acknowledge: false,
      roomId: 'room',
      assetId: 'secret'
    });
    await vi.waitFor(() => expect(getToasts().at(-1)?.action?.label).toBe('Undo'));
    getToasts().at(-1)?.action?.onClick();
    await vi.waitFor(() => expect(assets.undoAttachmentPermanence).toHaveBeenCalled());
    expect(receivedRequest(assets.undoAttachmentPermanence)).toMatchObject({ undoToken: 'undo' });
    await vi.waitFor(() =>
      expect(view.onchange).toHaveBeenLastCalledWith(
        expect.objectContaining({
          burn: expect.objectContaining({
            viewerStatus: 'burned',
            unopenedExpiresAt: '2099-01-01T00:00:00.000Z'
          })
        })
      )
    );
  });
  it('explains future-member access before the first conversion in a conversation', async () => {
    assets.getAsset.mockReturnValue({ asset: original(true) });
    const view = mount(original(true));
    await view.getByRole('button', { name: 'Make permanent', exact: true }).click();
    await expect.element(view.getByText('Make this attachment permanent?')).toBeVisible();
    expect(assets.makeAttachmentPermanent).not.toHaveBeenCalled();
    await expect.element(view.getByText(/including future members/)).toBeVisible();
    await view
      .getByRole('dialog')
      .getByRole('button', { name: 'Make permanent', exact: true })
      .click();
    await vi.waitFor(() =>
      expect(receivedRequest(assets.makeAttachmentPermanent)).toMatchObject({ acknowledge: true })
    );
  });
  it('allows a recipient to cancel a request and never presents the sender action', async () => {
    const asset = new Asset({
      id: 'secret',
      burn: {
        viewerStatus: BurnAttachmentViewerStatus.BURNED,
        canRequestPermanent: true,
        permanenceRequested: true
      }
    });
    assets.requestAttachmentPermanence.mockReturnValue({
      asset: new Asset({ ...asset, burn: { ...asset.burn, permanenceRequested: false } })
    });
    const view = mount(asset);
    await view.getByRole('button', { name: 'Request permanent access', exact: true }).click();
    await vi.waitFor(() =>
      expect(receivedRequest(assets.requestAttachmentPermanence)).toMatchObject({
        requested: false
      })
    );
    expect(view.container.textContent).not.toContain('Make permanent');
    expect(assets.makeAttachmentPermanent).not.toHaveBeenCalled();
  });

  it('shows the owner a request count and hydrates requester names through the shared profile store', async () => {
    const users = mockService(UserService);
    users.batchGetUsers.mockReturnValue({
      users: [{ user: { id: 'bob', login: 'bob', displayName: 'Bob' } }]
    });
    const profiles = getUserStore('request-owner', 'request-list');
    profiles.clear();
    createTestServerScope({
      serverId: 'request-owner',
      queryScope: 'request-list',
      routes: (router) => router.service(AssetService, assets).service(UserService, users),
      store: { projection: { users: profiles } }
    });
    const view = mount(
      new Asset({ ...original(), burn: { ...original().burn, requesterIds: ['bob'] } })
    );
    await view.getByRole('button', { name: '1 requests', exact: true }).click();
    await expect.element(view.getByRole('dialog').getByText('Bob', { exact: true })).toBeVisible();
    expect(receivedRequest(users.batchGetUsers)).toMatchObject({ userIds: ['bob'] });
    expect(assets.makeAttachmentPermanent).not.toHaveBeenCalled();
  });
});
