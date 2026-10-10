package core

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"hmans.de/chatto/internal/config"
	evtv1 "hmans.de/chatto/internal/pb/chatto/core/evt/v1"
)

func postBurnTestAttachment(t *testing.T, c *ChattoCore, ctx context.Context, userID, roomID string) string {
	t.Helper()
	asset := uploadRoomAttachmentForUser(t, c, ctx, userID, roomID, "view-once.png")
	_, err := c.Messages().PostMessage(ctx, MessagePostInput{ActorID: userID, RoomID: roomID,
		AttachmentAssetIDs: []string{asset.Id}, BurnAttachmentAssetIDs: []string{asset.Id}})
	require.NoError(t, err)
	return asset.Id
}

func TestBurnAttachmentAudienceAndIndependentSessions(t *testing.T) {
	for _, kind := range []RoomKind{KindChannel, KindDM} {
		t.Run(string(kind), func(t *testing.T) {
			c, _ := setupTestCore(t)
			ctx := testContext(t)
			owner, recipient, room := setupAttachmentOwnershipUsers(t, c, ctx, "burn-"+string(kind))
			if kind == KindDM {
				var err error
				room, _, err = c.FindOrCreateDM(ctx, owner.Id, []string{recipient.Id})
				require.NoError(t, err)
			}
			assetID := postBurnTestAttachment(t, c, ctx, owner.Id, room.Id)
			state := c.GetAssetState(assetID).Burn
			require.ElementsMatch(t, []string{owner.Id, recipient.Id}, state.RecipientIds)
			require.Equal(t, "available", c.BurnAttachmentMetadata(assetID, recipient.Id).Status)
			input := BurnAttachmentInput{ActorID: recipient.Id, RoomID: room.Id, AssetID: assetID, SessionID: "recipient-session-123"}
			_, err := c.OpenBurnAttachment(ctx, input)
			require.NoError(t, err)
			_, err = c.OpenBurnAttachment(ctx, input) // lost response retry
			require.NoError(t, err)
			require.Len(t, c.GetAssetState(assetID).Burn.Views, 1)
			_, err = c.AuthorizeAssetBinary(ctx, assetID, recipient.Id, input.SessionID, false)
			require.NoError(t, err)
			_, err = c.AuthorizeAssetBinary(ctx, assetID, recipient.Id, input.SessionID, true)
			require.ErrorIs(t, err, ErrPermissionDenied)
			_, err = c.AuthorizeAssetBinary(ctx, assetID, owner.Id, input.SessionID, false)
			require.ErrorIs(t, err, ErrPermissionDenied)
			_, err = c.CloseBurnAttachment(ctx, input)
			require.NoError(t, err)
			_, err = c.CloseBurnAttachment(ctx, input)
			require.NoError(t, err)
			_, err = c.OpenBurnAttachment(ctx, input)
			require.ErrorIs(t, err, ErrPermissionDenied)
			_, err = c.AuthorizeAssetBinary(ctx, assetID, recipient.Id, input.SessionID, false)
			require.ErrorIs(t, err, ErrPermissionDenied)
			require.Equal(t, "burned", c.BurnAttachmentMetadata(assetID, recipient.Id).Status)
			input.ActorID, input.SessionID = owner.Id, "owner-session-123456"
			_, err = c.OpenBurnAttachment(ctx, input)
			require.NoError(t, err) // another person's session remains available
			if kind == KindChannel {
				late, err := c.CreateUser(ctx, SystemActorID, "burn-late", "Late member", "password123")
				require.NoError(t, err)
				_, err = c.JoinRoom(ctx, late.Id, kind, late.Id, room.Id)
				require.NoError(t, err)
				input.ActorID, input.SessionID = late.Id, "late-session-1234567"
				_, err = c.OpenBurnAttachment(ctx, input)
				require.ErrorIs(t, err, ErrPermissionDenied)
				require.Equal(t, "ineligible", c.BurnAttachmentMetadata(assetID, late.Id).Status)
				input.Requested = true
				_, err = c.RequestAttachmentPermanence(ctx, input)
				require.ErrorIs(t, err, ErrPermissionDenied)
			}
		})
	}
}

func TestBurnAttachmentConcurrentClaimsAcrossReplicas(t *testing.T) {
	c, nc := setupTestCore(t)
	ctx := testContext(t)
	owner, recipient, room := setupAttachmentOwnershipUsers(t, c, ctx, "burn-race")
	assetID := postBurnTestAttachment(t, c, ctx, owner.Id, room.Id)
	replica, err := NewChattoCore(ctx, nc, config.CoreConfig{SecretKey: "test-core-secret", Assets: config.AssetsConfig{SigningSecret: "test-signing-secret"}})
	require.NoError(t, err)
	startCoreServices(t, replica)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for i, node := range []*ChattoCore{c, replica} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := node.OpenBurnAttachment(ctx, BurnAttachmentInput{ActorID: recipient.Id, RoomID: room.Id, AssetID: assetID, SessionID: []string{"device-one-12345678", "device-two-12345678"}[i]})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	successes, denied := 0, 0
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrPermissionDenied) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, denied)
	state, err := c.FreshAssetState(ctx, assetID)
	require.NoError(t, err)
	require.Len(t, state.Burn.Views, 1)
}

func TestBurnAttachmentRequestsPermanenceAndUndoPreserveHistory(t *testing.T) {
	c, _ := setupTestCore(t)
	ctx := testContext(t)
	owner, recipient, room := setupAttachmentOwnershipUsers(t, c, ctx, "burn-permanence")
	assetID := postBurnTestAttachment(t, c, ctx, owner.Id, room.Id)
	input := BurnAttachmentInput{ActorID: recipient.Id, RoomID: room.Id, AssetID: assetID, SessionID: "viewer-session-12345", Requested: true}
	_, err := c.OpenBurnAttachment(ctx, input)
	require.NoError(t, err)
	_, err = c.CloseBurnAttachment(ctx, input)
	require.NoError(t, err)
	before := c.GetAssetState(assetID).Burn
	_, err = c.RequestAttachmentPermanence(ctx, input)
	require.NoError(t, err)
	_, err = c.RequestAttachmentPermanence(ctx, input)
	require.NoError(t, err)
	require.Equal(t, []string{recipient.Id}, c.BurnAttachmentMetadata(assetID, owner.Id).RequesterIDs)
	require.Empty(t, c.BurnAttachmentMetadata(assetID, recipient.Id).RequesterIDs)
	input.Requested = false
	_, err = c.RequestAttachmentPermanence(ctx, input)
	require.NoError(t, err)
	require.Empty(t, c.GetAssetState(assetID).Burn.RequesterIds)
	_, err = c.MakeAttachmentPermanent(ctx, input)
	require.ErrorIs(t, err, ErrPermissionDenied)
	input.ActorID = owner.Id
	_, err = c.MakeAttachmentPermanent(ctx, input)
	require.Error(t, err) // first-use explanation required
	input.Acknowledge = true
	result, err := c.MakeAttachmentPermanent(ctx, input)
	require.NoError(t, err)
	require.True(t, result.Burn.Permanent)
	_, err = c.AuthorizeAssetBinary(ctx, assetID, recipient.Id, "", true)
	require.NoError(t, err)
	input.UndoToken = "wrong-token"
	_, err = c.UndoAttachmentPermanence(ctx, input)
	require.ErrorIs(t, err, ErrPermissionDenied)
	input.UndoToken = result.Burn.PermanentEventId
	_, err = c.UndoAttachmentPermanence(ctx, input)
	require.NoError(t, err)
	after := c.GetAssetState(assetID).Burn
	require.True(t, proto.Equal(before.UnopenedExpiresAt, after.UnopenedExpiresAt))
	require.True(t, proto.Equal(before.Views[0], after.Views[0]))
	require.Equal(t, before.RecipientIds, after.RecipientIds)
	require.Equal(t, "burned", c.BurnAttachmentMetadata(assetID, recipient.Id).Status)
	_, err = c.AuthorizeAssetBinary(ctx, assetID, recipient.Id, input.SessionID, false)
	require.ErrorIs(t, err, ErrPermissionDenied)
	otherID := postBurnTestAttachment(t, c, ctx, owner.Id, room.Id)
	require.False(t, c.BurnAttachmentMetadata(otherID, owner.Id).RequiresPermanenceConfirmation)
	input.AssetID, input.Acknowledge = otherID, false
	_, err = c.MakeAttachmentPermanent(ctx, input)
	require.NoError(t, err) // acknowledgement persists per sender/room
}

func TestBurnRecoveryDeadlineUsesOriginalSessionEnds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state := &evtv1.AssetBurnState{RecipientIds: []string{"one", "two"}, UnopenedExpiresAt: timestamppb.New(now.Add(time.Hour)), RecoveryDurationMs: int64(time.Hour / time.Millisecond)}
	require.Nil(t, burnDeleteAt(state, now))
	state.Views = []*evtv1.AssetBurnView{{UserId: "one", ExpiresAt: timestamppb.New(now.Add(5 * time.Minute)), ClosedAt: timestamppb.New(now.Add(-time.Minute))}}
	require.Nil(t, burnDeleteAt(state, now)) // unread second recipient still owns a session
	state.Views = append(state.Views, &evtv1.AssetBurnView{UserId: "two", ExpiresAt: timestamppb.New(now.Add(-time.Second))})
	expected := now.Add(-time.Second).Add(time.Hour)
	require.Equal(t, expected, burnDeleteAt(state, now).AsTime())
	state.RequesterIds, state.Permanent = []string{"two"}, true
	require.Equal(t, expected, burnDeleteAt(state, now.Add(2*time.Hour)).AsTime())
	state.Permanent = false
	require.False(t, burnRetained(state, now.Add(2*time.Hour)))
}

func TestBurnExpiryAndPermanenceUseSameAssetFence(t *testing.T) {
	c, _ := setupTestCore(t)
	ctx := testContext(t)
	owner, _, room := setupAttachmentOwnershipUsers(t, c, ctx, "burn-expiry")
	assetID := postBurnTestAttachment(t, c, ctx, owner.Id, room.Id)
	state := c.GetAssetState(assetID).Burn
	child, err := c.UploadDerivativeAttachment(ctx, assetID, evtv1.AssetDerivativeRole_ASSET_DERIVATIVE_ROLE_THUMBNAIL, room.Id, "thumb.png", "image/png", bytes.NewReader(createTestPNG(8, 8)))
	require.NoError(t, err)
	deadline := state.UnopenedExpiresAt.AsTime().Add(time.Duration(state.RecoveryDurationMs) * time.Millisecond)
	result, err := c.MakeAttachmentPermanent(ctx, BurnAttachmentInput{ActorID: owner.Id, RoomID: room.Id, AssetID: assetID, Acknowledge: true})
	require.NoError(t, err)
	require.NoError(t, c.assetModel.expireBurnAttachment(ctx, assetID, deadline.Add(time.Second)))
	require.False(t, c.GetAssetState(assetID).Deleted) // stale expiry candidate cannot delete normal file
	_, err = c.UndoAttachmentPermanence(ctx, BurnAttachmentInput{ActorID: owner.Id, RoomID: room.Id, AssetID: assetID, UndoToken: result.Burn.PermanentEventId})
	require.NoError(t, err)
	require.NoError(t, c.assetModel.expireBurnAttachment(ctx, assetID, deadline.Add(time.Second)))
	require.True(t, c.GetAssetState(assetID).Deleted)
	_, err = c.AuthorizeAssetBinary(ctx, child.Id, owner.Id, "", false)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, consumeAssetCleanupForTest(ctx, restartAssetModel(t, c)))
	require.True(t, c.GetAssetState(child.Id).Deleted)
	require.NoError(t, consumeAssetCleanupForTest(ctx, restartAssetModel(t, c)))
	_, _, err = c.GetAttachmentReader(ctx, child)
	require.Error(t, err)
	// A slow processing worker can publish a derivative after the parent cleanup.
	late, err := c.UploadDerivativeAttachment(ctx, assetID, evtv1.AssetDerivativeRole_ASSET_DERIVATIVE_ROLE_THUMBNAIL, room.Id, "late.png", "image/png", bytes.NewReader(createTestPNG(8, 8)))
	require.NoError(t, err)
	require.Contains(t, c.assetModel.assets.Projection().orphanDerivativeAssetIDs(), late.Id)
	// A restarted deadline worker repairs late creations without needing the
	// already-acknowledged source deletion delivery again.
	repairCtx, stopRepair := context.WithCancel(ctx)
	defer stopRepair()
	repairDone := make(chan error, 1)
	go func() { repairDone <- restartAssetModel(t, c).runBurnExpiry(repairCtx) }()
	require.Eventually(t, func() bool { return c.GetAssetState(late.Id).Deleted }, 2*time.Second, 10*time.Millisecond)
	stopRepair()
	require.NoError(t, <-repairDone)
	require.NoError(t, consumeAssetCleanupForTest(ctx, restartAssetModel(t, c)))
	_, _, err = c.GetAttachmentReader(ctx, late)
	require.Error(t, err)
	_, err = c.MakeAttachmentPermanent(ctx, BurnAttachmentInput{ActorID: owner.Id, RoomID: room.Id, AssetID: assetID})
	require.ErrorIs(t, err, ErrNotFound) // conversion cannot resurrect tombstone
}

func TestBurnProjectionReplaySnapshotAndDeletionKeepConsumedSessions(t *testing.T) {
	p := NewAssetProjection()
	state := &evtv1.AssetBurnState{AssetId: "A1", RoomId: "R1", UserId: "U1", MessageEventId: "M1", RecipientIds: []string{"U1", "U2"},
		UnopenedExpiresAt: timestamppb.New(time.Now().Add(time.Hour)), ViewDurationMs: 300000, RecoveryDurationMs: 3600000}
	created := testCoreAssetCreatedEvent("R1", "A1", "image/png")
	attached := &evtv1.Event{Id: "attached", Event: &evtv1.Event_AssetAttached{AssetAttached: &evtv1.AssetAttachedEvent{AssetId: "A1", RoomId: "R1", UserId: "U1", MessageEventId: "M1", Burn: state}}}
	require.NoError(t, p.Apply(created, 1))
	require.NoError(t, p.Apply(attached, 2))
	state = proto.Clone(state).(*evtv1.AssetBurnState)
	ended := timestamppb.Now()
	state.Views = []*evtv1.AssetBurnView{{UserId: "U2", SessionHash: burnSessionHash("session-123456789"), ExpiresAt: ended, ClosedAt: ended}}
	state.PermanenceAcknowledged = true
	updated := &evtv1.Event{Id: "updated", Event: &evtv1.Event_AssetBurnUpdated{AssetBurnUpdated: &evtv1.AssetBurnUpdatedEvent{State: state}}}
	require.NoError(t, p.Apply(updated, 3))
	data, err := p.Snapshot()
	require.NoError(t, err)
	restored := NewAssetProjection()
	require.NoError(t, restored.Restore(data))
	require.True(t, proto.Equal(p.AssetState("A1").Burn, restored.AssetState("A1").Burn))
	require.NoError(t, restored.Apply(updated, 3))
	require.NoError(t, restored.Apply(attached, 2)) // replay cannot reset the history
	require.Len(t, restored.AssetState("A1").Burn.Views, 1)
	detached := restored.AssetState("A1").Burn
	detached.Views = nil
	require.Len(t, restored.AssetState("A1").Burn.Views, 1)
	require.NoError(t, restored.Apply(&evtv1.Event{Id: "deleted", Event: &evtv1.Event_AssetDeleted{AssetDeleted: &evtv1.AssetDeletedEvent{AssetId: "A1"}}}, 4))
	require.True(t, restored.permanenceAcknowledged("U1", "R1"))
	require.True(t, restored.AssetState("A1").Deleted)
	require.Empty(t, restored.expiredBurnAssetIDs(time.Now().Add(24*time.Hour)))
}

func TestBurnAttachmentCannotBeSelectedOutsidePostedAssets(t *testing.T) {
	c, _ := setupTestCore(t)
	ctx := testContext(t)
	owner, _, room := setupAttachmentOwnershipUsers(t, c, ctx, "burn-selection")
	_, err := c.Messages().PostMessage(ctx, MessagePostInput{ActorID: owner.Id, RoomID: room.Id, Body: "hello", BurnAttachmentAssetIDs: []string{"unrelated"}})
	require.Error(t, err)
}

func TestBurnAttachmentSupportedTypes(t *testing.T) {
	for _, typ := range []string{"image/png", "text/plain; charset=utf-8", "application/pdf", "video/mp4", "audio/ogg"} {
		require.True(t, BurnAttachmentSupported(typ, "file"), typ)
	}
	for _, typ := range []string{"image/svg+xml", "text/html", "application/xhtml+xml", "application/zip"} {
		require.False(t, BurnAttachmentSupported(typ, "file"), typ)
	}
}
