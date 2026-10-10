package core

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	evtv1 "hmans.de/chatto/internal/pb/chatto/core/evt/v1"
)

func TestDMVisibilityPrivateDurableAndRestorable(t *testing.T) {
	c, _ := setupTestCore(t)
	ctx := testContext(t)
	owner, err := c.CreateUser(ctx, SystemActorID, "visibility-owner", "Owner", "password123")
	require.NoError(t, err)
	peer, err := c.CreateUser(ctx, SystemActorID, "visibility-peer", "Peer", "password123")
	require.NoError(t, err)
	outsider, err := c.CreateUser(ctx, SystemActorID, "visibility-outsider", "Outsider", "password123")
	require.NoError(t, err)
	dm, _, err := c.FindOrCreateDM(ctx, owner.Id, []string{peer.Id})
	require.NoError(t, err)
	beforeReplay, err := c.PlanRealtimeReplay(ctx, owner.Id, "")
	require.NoError(t, err)
	settings, err := c.SetDMVisibility(ctx, owner.Id, dm.Id, true)
	require.NoError(t, err)
	require.Equal(t, []string{dm.Id}, settings.GetHiddenDmRoomIds())
	replay, err := c.PlanRealtimeReplay(ctx, owner.Id, beforeReplay.BoundaryCursor)
	require.NoError(t, err)
	require.False(t, replay.Reset)
	var visibility *evtv1.Event
	for _, envelope := range replay.Events {
		if event := envelope.EVTEvent(); event != nil && event.GetUserDmVisibilityChanged() != nil {
			visibility = event
		}
	}
	require.NotNil(t, visibility, "reconnect replay must include private DM visibility")
	require.True(t, isDeliverableLiveEVTUserConfigEvent(visibility))
	require.Equal(t, owner.Id, userIDOfUserConfigEvent(visibility))

	require.Equal(t, []string{dm.Id}, func() []string {
		prefs, err := c.GetUserSettings(ctx, owner.Id)
		require.NoError(t, err)
		return prefs.GetHiddenDmRoomIds()
	}())
	// The partner's preference is independent and an outsider cannot change one.
	peerSettings, err := c.GetUserSettings(ctx, peer.Id)
	require.NoError(t, err)
	require.Empty(t, peerSettings.GetHiddenDmRoomIds())
	_, err = c.SetDMVisibility(ctx, outsider.Id, dm.Id, true)
	require.ErrorIs(t, err, ErrNotRoomMember)
	_, err = c.SetDMVisibility(ctx, "", dm.Id, true)
	require.Error(t, err)
	_, err = c.SetDMVisibility(ctx, owner.Id, "missing", true)
	require.ErrorIs(t, err, ErrNotFound)
	channel, err := c.CreateRoom(ctx, SystemActorID, KindChannel, "", "visibility-channel", "", WithUniversalRoom(true))
	require.NoError(t, err)
	_, err = c.SetDMVisibility(ctx, owner.Id, channel.Id, true)
	require.Error(t, err)
	// Repeating the command is a no-op: there is no additional durable fact.
	filter := "evt.config." + owner.Id + ".>"
	before, err := c.configModel.publisher.LastSubjectSeq(ctx, filter)
	require.NoError(t, err)
	_, err = c.SetDMVisibility(ctx, owner.Id, dm.Id, true)
	require.NoError(t, err)
	after, err := c.configModel.publisher.LastSubjectSeq(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, before, after)
	// StartDM restores the existing conversation regardless of the client UI.
	existing, created, err := c.RoomCommands().StartDM(ctx, RoomStartDMInput{ActorID: owner.Id, ParticipantIDs: []string{peer.Id}})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, dm.Id, existing.Id)
	settings, err = c.GetUserSettings(ctx, owner.Id)
	require.NoError(t, err)
	require.Empty(t, settings.GetHiddenDmRoomIds())
	// A deleted peer does not prevent hiding or restoring the surviving DM.
	require.NoError(t, c.DeleteUser(ctx, SystemActorID, peer.Id))
	_, err = c.SetDMVisibility(ctx, owner.Id, dm.Id, true)
	require.NoError(t, err)
	_, err = c.SetDMVisibility(ctx, owner.Id, dm.Id, false)
	require.NoError(t, err)
}

func TestDMVisibilityConcurrentConversationsMerge(t *testing.T) {
	c, _ := setupTestCore(t)
	ctx := testContext(t)
	owner, err := c.CreateUser(ctx, SystemActorID, "visibility-concurrent", "Owner", "password123")
	require.NoError(t, err)
	// A self-DM and a two-person DM exercise two changes in the same config lane.
	peer, err := c.CreateUser(ctx, SystemActorID, "visibility-concurrent-peer", "Peer", "password123")
	require.NoError(t, err)
	a, _, err := c.FindOrCreateDM(ctx, owner.Id, nil)
	require.NoError(t, err)
	b, _, err := c.FindOrCreateDM(ctx, owner.Id, []string{peer.Id})
	require.NoError(t, err)
	start := make(chan struct{})
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{a.Id, b.Id} {
		wg.Add(1)
		go func(roomID string) {
			defer wg.Done()
			<-start
			_, err := c.SetDMVisibility(ctx, owner.Id, roomID, true)
			failures <- err
		}(id)
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	prefs, err := c.GetUserSettings(ctx, owner.Id)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{a.Id, b.Id}, prefs.GetHiddenDmRoomIds())
}

func TestConfigProjectionDMVisibilityReplaySnapshotAndDeletion(t *testing.T) {
	p, model := newConfigProjectionUnderModel()
	apply := func(room string, hidden bool) {
		require.NoError(t, p.Apply(&evtv1.Event{Event: &evtv1.Event_UserDmVisibilityChanged{
			UserDmVisibilityChanged: &evtv1.UserDMVisibilityChangedEvent{UserId: "viewer", RoomId: room, Hidden: hidden},
		}}, 1))
	}
	apply("dm-b", true)
	apply("dm-a", true)
	apply("dm-b", true)
	prefs, ok := model.userSettings("viewer")
	require.True(t, ok)
	require.Equal(t, []string{"dm-a", "dm-b"}, prefs.GetHiddenDmRoomIds())
	prefs.HiddenDmRoomIds[0] = "tampered"
	snapshot, err := p.Snapshot()
	require.NoError(t, err)
	restored, restoredModel := newConfigProjectionUnderModel()
	require.NoError(t, restored.Restore(snapshot))
	prefs, ok = restoredModel.userSettings("viewer")
	require.True(t, ok)
	require.Equal(t, []string{"dm-a", "dm-b"}, prefs.GetHiddenDmRoomIds())
	apply("dm-b", false)
	prefs, _ = model.userSettings("viewer")
	require.Equal(t, []string{"dm-a"}, prefs.GetHiddenDmRoomIds())
	require.NoError(t, restored.Apply(&evtv1.Event{Event: &evtv1.Event_UserAccountDeleted{
		UserAccountDeleted: &evtv1.UserAccountDeletedEvent{UserId: "viewer"},
	}}, 2))
	_, ok = restoredModel.userSettings("viewer")
	require.False(t, ok)
}
