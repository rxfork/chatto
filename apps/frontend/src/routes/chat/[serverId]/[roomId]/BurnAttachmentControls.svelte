<!-- @component
Per-attachment reaction-style permanence actions. The server owns first-use
acknowledgement and eligibility; this component never stores either locally.
-->
<script lang="ts">
  import { onDestroy } from 'svelte';
  import type { MessageAttachmentView } from '@chatto/client/timeline/messageAttachments';
  import { createAttachmentAPI } from '@chatto/client/api/attachments';
  import { createUserAPI } from '@chatto/client/api/users';
  import { useServerScope } from '$lib/state/server/scope.svelte';
  import { createSessionGuard } from '$lib/state/server/sessionGuard.svelte';
  import { m } from '$lib/i18n/messages';
  import { toast } from '$lib/ui/toast';
  import { toastError } from '$lib/utils/errorMessage';
  import { ContextMenu, LoadingFog } from '$lib/ui';
  import { Button } from '$lib/ui/form';
  import AccountName from '$lib/components/users/AccountName.svelte';

  let {
    attachment,
    roomId,
    onchange
  }: {
    attachment: MessageAttachmentView;
    roomId: string;
    onchange: (attachment: MessageAttachmentView) => void;
  } = $props();
  const scope = useServerScope();
  const guard = createSessionGuard(scope);
  const api = scope.connection.getAPI(createAttachmentAPI);
  const burn = $derived(attachment.burn);
  let busy = $state(false);
  let popup = $state<'confirm' | 'requests' | null>(null);
  let anchor = $state<{ top: number; bottom: number; left: number } | null>(null);
  let requestsLoading = $state(false);
  let live = true;
  onDestroy(() => {
    live = false;
  });
  const requesters = $derived(
    (burn?.requesterIds ?? []).map((id) => ({ id, user: scope.store.projection.users.view(id) }))
  );

  function openPopup(kind: 'confirm' | 'requests', event: MouseEvent) {
    const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
    anchor = { top: rect.top, bottom: rect.bottom, left: rect.left };
    popup = kind;
    if (kind === 'requests') void hydrateRequesters();
  }
  async function hydrateRequesters() {
    const ids = burn?.requesterIds ?? [];
    if (!ids.length) return;
    requestsLoading = true;
    try {
      await scope.connection.getAPI(createUserAPI).batchGetUsers(ids);
    } catch (error) {
      toastError(error, m('room.attachment.burn.requesters_failed'));
    } finally {
      if (live) requestsLoading = false;
    }
  }
  async function requestPermanent() {
    if (busy || !burn) return;
    const snapshot = guard.snapshot(),
      original = attachment.id;
    busy = true;
    try {
      const fresh = await api.requestAttachmentPermanence(
        roomId,
        original,
        !burn.permanenceRequested
      );
      if (guard.isCurrent(snapshot) && attachment.id === original) onchange(fresh);
    } catch (error) {
      toastError(error, m('room.attachment.burn.request_failed'));
    } finally {
      if (guard.isCurrent(snapshot)) busy = false;
    }
  }
  async function choosePermanent(event: MouseEvent) {
    if (busy) return;
    const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
    const snapshot = guard.snapshot(),
      original = attachment.id;
    // A successful conversion in this conversation can acknowledge the
    // explanation for another attachment, including on another device.
    busy = true;
    try {
      const fresh = await api.getAttachment(roomId, original);
      if (!guard.isCurrent(snapshot) || attachment.id !== original) return;
      onchange(fresh);
      busy = false;
      if (fresh.burn?.requiresPermanenceConfirmation) {
        anchor = { top: rect.top, bottom: rect.bottom, left: rect.left };
        popup = 'confirm';
      } else await makePermanent(false);
    } catch (error) {
      toastError(error, m('room.attachment.burn.permanent_failed'));
    } finally {
      if (guard.isCurrent(snapshot)) busy = false;
    }
  }
  async function makePermanent(acknowledge: boolean) {
    if (busy) return;
    const snapshot = guard.snapshot(),
      original = attachment.id,
      targetRoom = roomId;
    busy = true;
    try {
      const result = await api.makeAttachmentPermanent(targetRoom, original, acknowledge);
      if (!guard.isCurrent(snapshot) || attachment.id !== original) return;
      popup = null;
      onchange(result.attachment);
      const duration = Math.max(0, new Date(result.undoExpiresAt).getTime() - Date.now());
      const toastId = toast.success(
        m('room.attachment.burn.made_permanent'),
        duration || 5000,
        result.undoToken && duration > 0
          ? {
              label: m('room.attachment.burn.undo'),
              onClick: () => {
                toast.remove(toastId);
                void undo(targetRoom, original, result.undoToken);
              }
            }
          : undefined
      );
    } catch (error) {
      toastError(error, m('room.attachment.burn.permanent_failed'));
    } finally {
      if (guard.isCurrent(snapshot)) busy = false;
    }
  }
  async function undo(targetRoom: string, assetId: string, token: string) {
    try {
      const fresh = await api.undoAttachmentPermanence(targetRoom, assetId, token);
      if (live && scope.isCurrent() && roomId === targetRoom && attachment.id === assetId)
        onchange(fresh);
      toast.success(m('room.attachment.burn.undo_success'));
    } catch (error) {
      toastError(error, m('room.attachment.burn.undo_failed'));
    }
  }
</script>

{#if burn?.viewerStatus === 'permanent'}
  <span class="mt-1 meta-badge text-muted" data-testid="attachment-permanent">
    <span class="iconify icon-[uil--unlock]" aria-hidden="true"></span>
    {m('room.attachment.burn.permanent')}
  </span>
{:else if burn}
  <div
    class="mt-1 flex max-w-full flex-wrap items-center gap-1"
    data-testid="burn-attachment-controls"
  >
    {#if burn.canMakePermanent}
      <button
        type="button"
        class="meta-badge min-h-10 cursor-pointer gap-1.5 px-3 text-sm disabled:cursor-not-allowed disabled:opacity-50"
        disabled={busy}
        title={m('room.attachment.burn.permanent_help')}
        aria-haspopup="dialog"
        aria-expanded={popup === 'confirm'}
        onclick={(event) => void choosePermanent(event)}
      >
        <span class="iconify icon-[uil--unlock]" aria-hidden="true"></span>
        {m('room.attachment.burn.make_permanent')}
      </button>
      {#if burn.requesterIds.length}
        <button
          type="button"
          class="meta-badge min-h-10 cursor-pointer px-3 text-sm"
          aria-expanded={popup === 'requests'}
          aria-haspopup="dialog"
          onclick={(event) => openPopup('requests', event)}
        >
          {m('room.attachment.burn.requests', { count: burn.requesterIds.length })}
        </button>
      {/if}
    {:else if burn.canRequestPermanent}
      <button
        type="button"
        class="meta-badge min-h-10 cursor-pointer gap-1.5 px-3 text-sm disabled:cursor-not-allowed disabled:opacity-50"
        aria-pressed={burn.permanenceRequested}
        aria-label={m('room.attachment.burn.request_permanent')}
        disabled={busy}
        onclick={() => void requestPermanent()}
        title={burn.permanenceRequested ? m('room.attachment.burn.cancel_request') : undefined}
      >
        {burn.permanenceRequested
          ? m('room.attachment.burn.requested')
          : m('room.attachment.burn.request_permanent')}
        {#if burn.permanenceRequested}<span class="iconify icon-[uil--check]" aria-hidden="true"
          ></span>{/if}
      </button>
    {/if}
  </div>
{/if}

{#if popup && anchor}
  <ContextMenu
    {anchor}
    role="dialog"
    ariaLabel={m(
      popup === 'confirm'
        ? 'room.attachment.burn.confirm_title'
        : 'room.attachment.burn.requesters_title'
    )}
    class="max-w-sm text-sm"
    onclose={() => {
      if (!busy) popup = null;
    }}
  >
    {#if popup === 'confirm'}
      <div class="flex flex-col gap-3 p-2">
        <p class="font-semibold">{m('room.attachment.burn.confirm_title')}</p>
        <p class="text-muted">{m('room.attachment.burn.permanent_help')}</p>
        <div class="flex flex-wrap gap-2">
          <Button size="sm" loading={busy} onclick={() => void makePermanent(true)}
            >{m('room.attachment.burn.make_permanent')}</Button
          >
          <Button size="sm" variant="secondary" disabled={busy} onclick={() => (popup = null)}
            >{m('common.cancel')}</Button
          >
        </div>
      </div>
    {:else}
      <div class="flex max-h-72 flex-col gap-2 overflow-y-auto p-2">
        <p class="font-semibold">{m('room.attachment.burn.requesters_title')}</p>
        {#if requestsLoading}<LoadingFog class="h-10 w-full" />{/if}
        <ul class="flex flex-col gap-2">
          {#each requesters as { id, user } (id)}
            <li>
              <AccountName
                name={user ? user.displayName || user.login : m('common.unknown_user')}
                identity={user}
              />
            </li>
          {/each}
        </ul>
        <Button size="sm" variant="secondary" onclick={() => (popup = null)}>{m('ui.close')}</Button
        >
      </div>
    {/if}
  </ContextMenu>
{/if}
