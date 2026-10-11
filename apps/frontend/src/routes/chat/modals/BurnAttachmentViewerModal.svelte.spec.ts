import '../../../app.css';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { afterEach } from 'vitest';
import { tick } from 'svelte';
import { RealtimeEvent } from '@chatto/api-types/realtime/v1/realtime_pb';
import { RealtimeProjectionUpdate } from '@chatto/client/realtime/eventBus';
import { render } from 'vitest-browser-svelte';
import { Timestamp } from '@bufbuild/protobuf';
import { Asset } from '@chatto/api-types/api/v1/attachments_pb';
import { BurnAttachmentViewerStatus } from '@chatto/api-types/api/v1/message_types_pb';
import { AssetService } from '@chatto/api-types/api/v1/attachments_connect';
import { mockService, receivedRequest } from '@chatto/client/testing/fakeServer';
import { createTestServerScope } from '$lib/test-utils/serverScope.svelte';
import { attachmentView } from '@chatto/client/api/roomTimeline';
import type { BurnAttachmentViewerModalState } from '$lib/modal';
import BurnAttachmentViewerModal from './BurnAttachmentViewerModal.svelte';

const mocks = vi.hoisted(() => ({ getStore: vi.fn(), getClient: vi.fn() }));
vi.mock('$lib/client', async () => ({
  ...(await import('$lib/test-utils/clientMock')).clientMockDefaults,
  serverRegistry: { getStore: mocks.getStore },
  serverConnectionManager: { getClient: mocks.getClient }
}));
const assets = mockService(AssetService);
const gif = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==';
function asset() {
  return new Asset({
    id: 'secret',
    filename: 'secret.gif',
    contentType: 'image/gif',
    burn: { viewerStatus: BurnAttachmentViewerStatus.AVAILABLE }
  });
}
let reset: (value: { privacy: boolean; retainView: boolean }) => void;
let update: (value: RealtimeProjectionUpdate) => void;
function mount(selected = asset()) {
  const modal: BurnAttachmentViewerModalState = {
    type: 'burnAttachmentViewer',
    serverId: 'server-1',
    roomId: 'room',
    eventId: 'message',
    attachment: attachmentView(selected)
  };
  const onclose = vi.fn();
  return { ...render(BurnAttachmentViewerModal, { modal, onclose }), modal, onclose };
}
describe('burn attachment viewer lifecycle', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    const scope = createTestServerScope({
      routes: (router) => router.service(AssetService, assets),
      store: {
        onReset: (callback: typeof reset) => {
          reset = callback;
          return () => {};
        },
        onUpdate: (callback: typeof update) => {
          update = callback;
          return () => {};
        }
      }
    });
    mocks.getStore.mockReturnValue(scope.scope.store);
    mocks.getClient.mockReturnValue(scope.scope.connection);
    assets.openBurnAttachment.mockReturnValue({
      asset: new Asset({
        ...asset(),
        assetUrl: { url: gif, expiresAt: Timestamp.fromDate(new Date('2099-01-01T00:00:00Z')) },
        burn: { viewerStatus: BurnAttachmentViewerStatus.VIEWING }
      }),
      viewExpiresAt: Timestamp.fromDate(new Date('2099-01-01T00:00:00Z'))
    });
    assets.closeBurnAttachment.mockReturnValue({
      asset: new Asset({ ...asset(), burn: { viewerStatus: BurnAttachmentViewerStatus.BURNED } })
    });
  });
  afterEach(() => vi.useRealTimers());
  it('claims only on explicit viewer mount, hides downloads and closes with the same in-memory session', async () => {
    const view = mount();
    await expect.element(view.getByRole('img', { name: 'secret.gif' })).toBeVisible();
    const opened = receivedRequest(assets.openBurnAttachment);
    expect(opened?.sessionId).toMatch(/^[\da-f-]{36}$/);
    expect(view.modal).not.toHaveProperty('sessionId');
    expect(view.container.querySelector('a[download]')).toBeNull();
    await view.getByRole('button', { name: 'Close', exact: true }).last().click();
    await vi.waitFor(() =>
      expect(receivedRequest(assets.closeBurnAttachment)).toMatchObject({
        sessionId: opened?.sessionId
      })
    );
    expect(view.container.querySelector('img')).toBeNull();
  });
  it('releases a successful claim whose response arrives after the viewer unmounts', async () => {
    let resolve!: (value: unknown) => void;
    // Keep a real generated-client command in flight at the component boundary.
    const deferred = new Promise<{ asset: Asset; viewExpiresAt: Timestamp }>((done) => {
      resolve = done as (value: unknown) => void;
    });
    assets.openBurnAttachment.mockImplementation(() => deferred);
    const view = mount();
    await vi.waitFor(() => expect(assets.openBurnAttachment).toHaveBeenCalled());
    const sessionId = receivedRequest(assets.openBurnAttachment)?.sessionId;
    await view.unmount();
    resolve({
      asset: asset(),
      viewExpiresAt: Timestamp.fromDate(new Date('2099-01-01T00:00:00Z'))
    });
    await vi.waitFor(() =>
      expect(receivedRequest(assets.closeBurnAttachment)).toMatchObject({ sessionId })
    );
  });

  it('removes decoded content and closes when a privacy reset arrives', async () => {
    const view = mount();
    await expect.element(view.getByRole('img', { name: 'secret.gif' })).toBeVisible();
    reset({ privacy: true, retainView: false });
    await tick();
    expect(view.container.querySelector('img')).toBeNull();
    expect(view.onclose).toHaveBeenCalledOnce();
    await vi.waitFor(() => expect(assets.closeBurnAttachment).toHaveBeenCalledOnce());
  });

  it('closes on Undo after a permanent conversion rather than retaining normal URLs', async () => {
    const view = mount();
    await expect.element(view.getByRole('img', { name: 'secret.gif' })).toBeVisible();
    const changed = new RealtimeProjectionUpdate({
      event: new RealtimeEvent({
        event: {
          case: 'attachmentChanged',
          value: { assetId: 'secret', roomId: 'room', messageEventId: 'message' }
        }
      })
    });
    assets.getAsset.mockReturnValue({
      asset: new Asset({
        ...asset(),
        assetUrl: { url: gif, expiresAt: Timestamp.fromDate(new Date('2099-01-01T00:00:00Z')) },
        burn: { viewerStatus: BurnAttachmentViewerStatus.PERMANENT }
      })
    });
    update(changed);
    await expect.element(view.getByText('Permanent', { exact: true })).toBeVisible();
    assets.getAsset.mockReturnValue({
      asset: new Asset({
        ...asset(),
        burn: {
          viewerStatus: BurnAttachmentViewerStatus.VIEWING,
          viewExpiresAt: Timestamp.fromDate(new Date('2099-01-01T00:00:00Z'))
        }
      })
    });
    update(changed);
    await vi.waitFor(() => expect(view.onclose).toHaveBeenCalledOnce());
    expect(view.container.querySelector('img')).toBeNull();
    await vi.waitFor(() => expect(assets.closeBurnAttachment).toHaveBeenCalledOnce());
  });

  it('reaches the fixed server deadline and removes the media', async () => {
    vi.useFakeTimers({
      toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval']
    });
    vi.setSystemTime(new Date('2026-10-09T12:00:00Z'));
    assets.openBurnAttachment.mockReturnValue({
      asset: new Asset({
        ...asset(),
        assetUrl: { url: gif },
        burn: { viewerStatus: BurnAttachmentViewerStatus.VIEWING }
      }),
      viewExpiresAt: Timestamp.fromDate(new Date('2026-10-09T12:01:00Z'))
    });
    const view = mount();
    await expect.element(view.getByRole('img', { name: 'secret.gif' })).toBeVisible();
    await vi.advanceTimersByTimeAsync(60_001);
    await tick();
    expect(view.container.querySelector('img')).toBeNull();
    await expect.element(view.getByRole('alert')).toHaveTextContent('Viewing ended');
    await vi.waitFor(() => expect(assets.closeBurnAttachment).toHaveBeenCalledOnce());
  });

  it('does not consume a PDF session when the browser has no native PDF viewer', async () => {
    Object.defineProperty(navigator, 'pdfViewerEnabled', { configurable: true, value: false });
    try {
      const view = mount(new Asset({ ...asset(), contentType: 'application/pdf' }));
      await expect
        .element(view.getByRole('alert'))
        .toHaveTextContent('This browser cannot display PDFs here.');
      expect(assets.openBurnAttachment).not.toHaveBeenCalled();
    } finally {
      Reflect.deleteProperty(navigator, 'pdfViewerEnabled');
    }
  });
});
