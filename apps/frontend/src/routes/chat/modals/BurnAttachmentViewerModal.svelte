<!-- @component
One explicit viewing session. The claim ID lives only in this viewer instance.
Closing, unmounting, access loss, or the server deadline removes media first.
-->
<script lang="ts">
  import { onMount, onDestroy, untrack } from 'svelte';
  import type { BurnAttachmentViewerModalState } from '$lib/modal';
  import type { MessageAttachmentView } from '@chatto/client/timeline/messageAttachments';
  import { supportsBurnAttachment } from '@chatto/client/timeline/messageAttachments';
  import { createAttachmentAPI } from '@chatto/client/api/attachments';
  import { assetUrlForServer } from '@chatto/client/util/assetUrls';
  import { serverConnectionManager, serverRegistry } from '$lib/client';
  import { Dialog, LoadingFog } from '$lib/ui';
  import { AttachmentPreview } from '$lib/ui/attachments';
  import { Button } from '$lib/ui/form';
  import Deadline from '$lib/lifecycle/Deadline.svelte';
  import Interval from '$lib/lifecycle/Interval.svelte';
  import { m } from '$lib/i18n/messages';
  import { errorMessage } from '$lib/utils/errorMessage';

  let { modal, onclose }: { modal: BurnAttachmentViewerModalState; onclose: () => void } = $props();
  const target = untrack(() => modal);
  const connection = serverConnectionManager.getClient(target.serverId);
  const api = connection.getAPI(createAttachmentAPI);
  // Never copy this value into history, query caches, or device storage.
  const sessionId = crypto.randomUUID();
  let item = $state.raw<MessageAttachmentView>(untrack(() => modal.attachment));
  let deadline = $state<string | null>(null);
  let now = $state(Date.now());
  const remaining = $derived(
    deadline ? Math.max(0, Math.ceil((Date.parse(deadline) - now) / 1000)) : 0
  );
  const remainingLabel = $derived(
    `${Math.floor(remaining / 60)}:${String(remaining % 60).padStart(2, '0')}`
  );
  let busy = $state(true);
  let error = $state<string | null>(null);
  let text = $state<string | null>(null);
  let showing = $state(false);
  let active = true;
  let claimPending = false;
  let claimSent = false;
  let released = false;
  let generation = 0;
  let reconciliation = 0;
  let readRequest: AbortController | null = null;
  const type = $derived(item.contentType.split(';', 1)[0].trim().toLowerCase());
  const pdf = $derived(type === 'application/pdf');
  const textFile = $derived(
    type.startsWith('text/') ||
      ['application/json', 'application/xml', 'application/markdown'].includes(type) ||
      ((!type || type === 'application/octet-stream') && /\.(md|markdown)$/i.test(item.filename))
  );
  const url = $derived(showing ? assetUrlForServer(target.serverId, item.assetUrl?.url) : null);

  onMount(() => {
    const store = serverRegistry.getStore(target.serverId);
    const unsubscribers = [
      store.onReset(({ privacy, retainView }) => {
        if (privacy || !retainView) close();
      }),
      store.onRoomAccessLost(({ roomId }) => {
        if (roomId === target.roomId) close();
      }),
      store.onSessionEnded(close),
      store.onUpdate(({ event }) => {
        const payload = event?.event;
        if (
          payload?.case === 'attachmentChanged' &&
          payload.value.roomId === target.roomId &&
          payload.value.assetId === item.id
        ) {
          // A permanent viewer holds ordinary media URLs. An Undo event can
          // revoke those before a metadata read returns, so remove them now.
          if (item.burn?.viewerStatus === 'permanent') close();
          else void reconcile();
        }
        if (
          payload?.case === 'messageRetracted' &&
          payload.value.roomId === target.roomId &&
          payload.value.messageEventId === target.eventId
        )
          end();
        if (
          payload?.case === 'assetDeleted' &&
          payload.value.roomId === target.roomId &&
          payload.value.assetId === item.id
        )
          end();
      })
    ];
    void open();
    return () => {
      for (const unsubscribe of unsubscribers) unsubscribe();
    };
  });
  onDestroy(() => {
    active = false;
    end();
  });

  /** Remove rendered bytes before trying the best-effort server close. */
  function end() {
    generation++;
    showing = false;
    text = null;
    item = { ...item, assetUrl: null, thumbnailAssetUrl: null, videoProcessing: null };
    readRequest?.abort();
    readRequest = null;
    deadline = null;
    if (active) {
      busy = false;
      error = m('room.attachment.burn.view_ended');
    }
    if (!claimPending) release();
  }
  function release() {
    if (!claimSent || released) return;
    released = true;
    void api.closeBurnAttachment(target.roomId, target.attachment.id, sessionId).catch(() => {
      // The server deadline also closes a session after connection loss.
    });
  }
  function close() {
    end();
    active = false;
    onclose();
  }

  async function open() {
    if (!supportsBurnAttachment(item.contentType, item.filename)) {
      busy = false;
      error = m('room.attachment.burn.unsupported');
      return;
    }
    // Native PDF support varies. Do not consume a session that cannot render.
    if (pdf && navigator.pdfViewerEnabled === false) {
      busy = false;
      error = m('room.attachment.burn.pdf_unavailable');
      return;
    }
    const selected = generation;
    claimPending = true;
    claimSent = true;
    try {
      const result = await api.openBurnAttachment(target.roomId, target.attachment.id, sessionId);
      if (!active || generation !== selected) return;
      item = { ...result.attachment, description: target.attachment.description };
      const expires = Date.parse(result.viewExpiresAt);
      if (!Number.isFinite(expires) || expires <= Date.now()) {
        end();
        return;
      }
      deadline = result.viewExpiresAt;
      now = Date.now();
      showing = true;
      await readText(selected);
    } catch (failure) {
      if (active && generation === selected)
        error = errorMessage(failure, m('room.attachment.burn.open_failed'));
    } finally {
      claimPending = false;
      if (active && generation === selected) busy = false;
      else release();
    }
  }
  async function readText(selected: number) {
    if (!textFile) return;
    const source = assetUrlForServer(target.serverId, item.assetUrl?.url);
    if (!source) throw new Error(m('room.attachment.html_viewer.preview_failed'));
    const controller = new AbortController();
    readRequest = controller;
    const response = await fetch(source, {
      signal: controller.signal,
      credentials: 'omit',
      referrerPolicy: 'no-referrer',
      cache: 'no-store'
    });
    if (!response.ok) throw new Error(m('room.attachment.html_viewer.preview_failed'));
    const body = await response.text();
    if (active && generation === selected && showing) text = body;
  }
  async function reconcile() {
    const selected = generation;
    const revision = ++reconciliation;
    try {
      const fresh = await api.getAttachment(target.roomId, item.id);
      if (!active || generation !== selected || reconciliation !== revision) return;
      const status = fresh.burn?.viewerStatus;
      if (status === 'permanent') {
        generation++;
        readRequest?.abort();
        readRequest = null;
        deadline = null;
        item = { ...fresh, description: item.description };
        showing = true;
        busy = false;
        error = null;
        await readText(generation);
      } else if (item.burn?.viewerStatus === 'permanent') {
        // Undo revokes ordinary URLs. Close this viewer before any decoded
        // content can keep using the old access mode or an elapsed deadline.
        close();
      } else if (status !== 'viewing') end();
      else deadline = fresh.burn?.viewExpiresAt ?? deadline;
    } catch {
      if (active && reconciliation === revision) end();
    }
  }
  async function mediaError(): Promise<string | null> {
    if (active) error = m('room.attachment.html_viewer.preview_failed');
    return null;
  }
</script>

<svelte:window onpagehide={end} />
<svelte:document
  onvisibilitychange={() => {
    // Browsers can delay timers in background tabs. Remove already-decoded
    // content before the first visible render after a delayed deadline.
    if (!document.hidden && deadline && Date.parse(deadline) <= Date.now()) end();
  }}
/>

{#if deadline}
  <Deadline at={deadline} onreached={end} />
  <Interval
    milliseconds={1000}
    ontick={() => {
      now = Date.now();
    }}
  />
{/if}
<Dialog visible title={item.filename} size="xl" mediaViewer onclose={close}>
  <div class="flex min-h-0 flex-1 flex-col gap-3" data-testid="burn-attachment-viewer">
    <p class="shrink-0 text-sm text-muted">
      {m(
        item.burn?.viewerStatus === 'permanent'
          ? 'room.attachment.burn.permanent'
          : 'room.attachment.burn.viewer_help'
      )}
    </p>
    {#if deadline}<p role="timer" aria-live="off" class="shrink-0 text-sm text-muted tabular-nums">
        {m('room.attachment.burn.remaining', { time: remainingLabel })}
      </p>{/if}
    {#if error}<p role="alert" class="text-sm text-danger">{error}</p>{/if}
    <div class="flex min-h-0 flex-1 items-center justify-center overflow-hidden rounded-lg">
      {#if busy}<LoadingFog class="h-full min-h-32 w-full" />
      {:else if showing && pdf && url}
        <iframe
          src={url}
          title={item.filename}
          referrerpolicy="no-referrer"
          class="h-full min-h-96 w-full border-0"
        ></iframe>
      {:else if showing && text !== null}
        <pre
          class="h-full w-full overflow-auto p-4 text-sm wrap-anywhere whitespace-pre-wrap"
          dir="auto">{text}</pre>
      {:else if showing && url && type.startsWith('audio/')}
        <audio
          src={url}
          controls
          controlslist="nodownload"
          preload="metadata"
          class="w-full max-w-lg"
          onerror={() => {
            void mediaError();
          }}>{item.filename}</audio
        >
      {:else if showing && url && type.startsWith('video/') && !item.videoProcessing}
        <video
          src={url}
          controls
          controlslist="nodownload"
          playsinline
          preload="metadata"
          class="h-full w-full object-contain"
          onerror={() => {
            void mediaError();
          }}><track kind="captions" /></video
        >
      {:else if showing}
        <AttachmentPreview
          {item}
          serverId={target.serverId}
          {url}
          busy={false}
          zoomable={type.startsWith('image/') && !item.videoProcessing}
          onpreview={() => {}}
          onerror={mediaError}
        />
      {/if}
    </div>
  </div>
  {#snippet footer()}
    <Button variant="secondary" onclick={close}>{m('ui.close')}</Button>
  {/snippet}
</Dialog>
