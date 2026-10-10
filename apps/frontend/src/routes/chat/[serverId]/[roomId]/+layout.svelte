<script lang="ts">
  import { untrack } from 'svelte';
  import { toast } from '$lib/ui/toast';
  import { errorMessage } from '$lib/utils/errorMessage';
  import { page } from '$app/state';
  import { serverUi } from '$lib/state/server/serverUi';
  import { roomRouteAccess } from '$lib/navigation/roomLinkAccess';
  import { useServerScope } from '$lib/state/server/scope.svelte';
  import Room from './Room.svelte';
  import RoomJoinScreen from './RoomJoinScreen.svelte';
  import { PageTitle } from '$lib/ui';

  let { data, children } = $props();

  let { roomId } = $derived(data);

  const serverScope = useServerScope();
  const activeServerId = serverScope.serverId;

  const serverStore = serverScope.store;
  const navigation = $derived(serverUi(serverStore).navigation);
  // Displayed membership belongs to this store's viewer. The connection owns
  // session verification and command readiness.
  const ready = $derived(
    !navigation.isInitialLoading &&
      (!serverStore.projectionViewerId || serverStore.projectionViewerId === serverStore.viewerId)
  );

  // Restore on a route/account transition, rather than on preference refreshes.
  // Another client may hide this conversation while it is already open here.
  let openedRoomId: string | undefined;
  let openedViewerId: string | null | undefined;
  $effect(() => {
    const id = roomId;
    const viewer = serverStore.accountId;
    if (id === openedRoomId && viewer === openedViewerId) return;
    openedRoomId = id;
    openedViewerId = viewer;
    if (id && viewer) {
      untrack(() => {
        if (serverStore.isDMHidden(id)) {
          void serverStore
            .setDMHidden(id, false)
            .catch((error) => toast.error(errorMessage(error)));
        }
      });
    }
  });

  let threadId = $derived(page.params.threadId);

  const isMessageLinkMode = $derived(page.route.id === '/chat/[serverId]/[roomId]/m/[messageId]');
  const roomAccess = $derived.by(() => {
    if (!ready || !roomId) return { kind: 'unknown' } as const;
    return roomRouteAccess({
      rooms: navigation.rooms,
      roomId
    });
  });
  const canRenderRoom = $derived(
    (ready || serverStore.realtimeSync.isRecoveringSnapshot) &&
      roomId &&
      (roomAccess.kind === 'member' || (roomAccess.kind === 'unknown' && !isMessageLinkMode))
  );
</script>

{#if ready && roomId && roomAccess.kind === 'nonmember'}
  {#key roomAccess.room.id}
    <RoomJoinScreen room={roomAccess.room} serverSegment={data.serverSegment} />
  {/key}
{:else if canRenderRoom && roomId}
  {#if isMessageLinkMode}
    <PageTitle />
    <!-- Message link resolver: renders +page.svelte which fetches + redirects -->
    {@render children?.()}
  {:else}
    <!--
			Room is rendered in the layout so it stays mounted when navigating
			between room and thread URLs. This prevents unnecessary reloads.
		-->
    {#key activeServerId}
      <Room {roomId} {threadId} routeMessageId={page.params.messageId} />
    {/key}
  {/if}
{:else}
  <PageTitle />
{/if}
