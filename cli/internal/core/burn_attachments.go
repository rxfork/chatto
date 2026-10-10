package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"hmans.de/chatto/internal/evtstream"
	evtv1 "hmans.de/chatto/internal/pb/chatto/core/evt/v1"
	"hmans.de/chatto/pkg/events"
)

const attachmentPermanenceUndoTTL = 15 * time.Second

// BurnAttachmentSupported reports file types that the bundled controlled
// viewer can display without handing the file to an external application.
// Saving or capturing bytes that have already been delivered remains possible.
func BurnAttachmentSupported(contentType, filename string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err == nil {
		contentType = strings.ToLower(mediaType)
	}
	switch contentType {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "image/avif", "image/bmp",
		"application/pdf", "application/json", "application/xml", "text/xml", "text/plain", "text/markdown", "text/x-markdown":
		return true
	}
	if strings.HasPrefix(contentType, "audio/") || strings.HasPrefix(contentType, "video/") {
		return true
	}
	return (contentType == "application/octet-stream" || contentType == "") &&
		(strings.HasSuffix(strings.ToLower(filename), ".md") || strings.HasSuffix(strings.ToLower(filename), ".markdown"))
}

// BurnAttachmentInput is an authorized operation on one message attachment.
// SessionID is a caller-generated random capability, kept only for one opening.
type BurnAttachmentInput struct {
	ActorID, RoomID, AssetID, SessionID string
	Requested, Acknowledge              bool
	UndoToken                           string
}

// BurnAttachmentResult returns detached metadata after a committed operation.
type BurnAttachmentResult struct {
	Attachment *evtv1.Attachment
	Burn       *evtv1.AssetBurnState
}

// BurnAttachmentView describes one viewer without exposing others' view history.
type BurnAttachmentView struct {
	Status                                                                                     string
	UnopenedExpiresAt, DeleteAt, ViewExpiresAt                                                 *timestamppb.Timestamp
	CanMakePermanent, CanRequestPermanent, PermanenceRequested, RequiresPermanenceConfirmation bool
	RequesterIDs                                                                               []string
}

func burnSessionHash(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(digest[:])
}

func burnView(state *evtv1.AssetBurnState, userID string) *evtv1.AssetBurnView {
	for _, view := range state.GetViews() {
		if view.GetUserId() == userID {
			return view
		}
	}
	return nil
}

// burnDeleteAt derives a fixed recovery deadline from the original sessions.
// No recipient request, configuration change, or undo can move this deadline.
func burnDeleteAt(state *evtv1.AssetBurnState, now time.Time) *timestamppb.Timestamp {
	if state == nil || state.GetUnopenedExpiresAt() == nil {
		return nil
	}
	lastEnd := time.Time{}
	for _, userID := range state.GetRecipientIds() {
		end := state.GetUnopenedExpiresAt().AsTime()
		if view := burnView(state, userID); view != nil {
			end = view.GetExpiresAt().AsTime()
			if view.GetClosedAt() != nil {
				end = view.GetClosedAt().AsTime()
			}
		}
		if end.After(now) {
			return nil
		}
		if end.After(lastEnd) {
			lastEnd = end
		}
	}
	if lastEnd.IsZero() {
		lastEnd = state.GetUnopenedExpiresAt().AsTime()
		if lastEnd.After(now) {
			return nil
		}
	}
	return timestamppb.New(lastEnd.Add(time.Duration(state.GetRecoveryDurationMs()) * time.Millisecond))
}

func burnRetained(state *evtv1.AssetBurnState, now time.Time) bool {
	deadline := burnDeleteAt(state, now)
	return deadline == nil || deadline.AsTime().After(now)
}

// FreshAssetState catches the local projection up to the authoritative asset
// tail. All HTTP binary access uses this boundary, including original tickets
// issued before a burn policy was attached or restored by Undo.
func (c *AssetModel) FreshAssetState(ctx context.Context, assetID string) (AssetState, error) {
	if c == nil || c.EventPublisher == nil {
		return AssetState{}, fmt.Errorf("asset model is not initialized")
	}
	tail, err := c.EventPublisher.LastSubjectPosition(ctx, evtstream.AssetAggregate(assetID).AllEventsFilter())
	if err != nil {
		return AssetState{}, err
	}
	if !tail.IsZero() {
		if err := c.waitForAssets(ctx, tail); err != nil {
			return AssetState{}, err
		}
	}
	return c.AssetState(assetID), nil
}

func (p *AssetProjection) permanenceAcknowledged(userID, roomID string) bool {
	p.RLock()
	defer p.RUnlock()
	for _, state := range p.burnStates {
		if state.GetUserId() == userID && state.GetRoomId() == roomID && state.GetPermanenceAcknowledged() {
			return true
		}
	}
	return false
}

func (p *AssetProjection) expiredBurnAssetIDs(now time.Time) []string {
	p.RLock()
	defer p.RUnlock()
	var ids []string
	for id, state := range p.burnStates {
		if _, deleted := p.deletedAssets[id]; !deleted && !state.GetPermanent() && !burnRetained(state, now) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func (p *AssetProjection) derivativeAssetIDs(parentID string) []string {
	p.RLock()
	defer p.RUnlock()
	var ids []string
	for id, creation := range p.assetCreations {
		if creation.GetParentAssetId() == parentID {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// A worker may finish a derivative after its source deletion was processed.
// Creation facts retain the parent, so this repair also survives that crash.
func (p *AssetProjection) orphanDerivativeAssetIDs() []string {
	p.RLock()
	defer p.RUnlock()
	var ids []string
	for id, creation := range p.assetCreations {
		if _, deleted := p.deletedAssets[creation.GetParentAssetId()]; deleted && creation.GetParentAssetId() != "" {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// BurnAttachmentMetadata is called only after the enclosing asset or message
// has been authorized. It provides metadata without granting a viewing URL.
func (c *AssetModel) BurnAttachmentMetadata(assetID, viewerID string) *BurnAttachmentView {
	state := c.AssetState(assetID)
	burn := state.Burn
	if burn == nil {
		return nil
	}
	now := time.Now()
	view := &BurnAttachmentView{UnopenedExpiresAt: burn.GetUnopenedExpiresAt()}
	if burn.GetPermanent() && !state.Deleted {
		view.Status = "permanent"
		return view
	}
	view.DeleteAt = burnDeleteAt(burn, now)
	retained := !state.Deleted && burnRetained(burn, now)
	view.CanMakePermanent = retained && burn.GetUserId() == viewerID
	if view.CanMakePermanent {
		view.RequesterIDs = slices.Clone(burn.GetRequesterIds())
		view.RequiresPermanenceConfirmation = !c.assets.Projection().permanenceAcknowledged(viewerID, burn.GetRoomId())
	}
	eligible := slices.Contains(burn.GetRecipientIds(), viewerID)
	view.CanRequestPermanent = retained && eligible && viewerID != burn.GetUserId()
	view.PermanenceRequested = slices.Contains(burn.GetRequesterIds(), viewerID)
	switch {
	case !retained:
		view.Status = "purged"
	case !eligible:
		view.Status = "ineligible"
	default:
		session := burnView(burn, viewerID)
		if session != nil {
			if session.GetClosedAt() == nil && session.GetExpiresAt().AsTime().After(now) {
				view.Status = "viewing"
				view.ViewExpiresAt = session.GetExpiresAt()
			} else {
				view.Status = "burned"
			}
		} else if !burn.GetUnopenedExpiresAt().AsTime().After(now) {
			view.Status = "expired"
		} else {
			view.Status = "available"
		}
	}
	return view
}

type burnMutation func(*evtv1.AssetBurnState, time.Time, string) (bool, error)

// mutateBurn serializes recipient claims, permanence, and cleanup on the same
// asset aggregate. Every retry rebuilds its decision and authorization.
func (c *AssetModel) mutateBurn(ctx context.Context, input BurnAttachmentInput, mutate burnMutation) (*BurnAttachmentResult, error) {
	if input.ActorID == "" || input.RoomID == "" || input.AssetID == "" {
		return nil, invalidArgument("room, asset, and actor are required")
	}
	agg := evtstream.AssetAggregate(input.AssetID)
	filter := agg.AllEventsFilter()
	for attempt := 0; attempt < maxAssetMutationAttempts; attempt++ {
		tail, err := c.EventPublisher.LastSubjectPosition(ctx, filter)
		if err != nil {
			return nil, err
		}
		if !tail.IsZero() {
			if err := c.waitForAssets(ctx, tail); err != nil {
				return nil, err
			}
		}
		state := c.AssetState(input.AssetID)
		if state.Deleted || state.Creation == nil || state.RoomID != input.RoomID || state.Burn == nil {
			return nil, ErrNotFound
		}
		if err := c.authorizeAtStableInputs(ctx, func() error {
			_, _, err := c.requireMessageReader(ctx, input.ActorID, input.RoomID, state.Burn.GetMessageEventId())
			return err
		}); err != nil {
			return nil, err
		}
		now := time.Now()
		if !state.Burn.GetPermanent() && !burnRetained(state.Burn, now) {
			return nil, ErrNotFound
		}
		event := newEvent(input.ActorID, &evtv1.Event{Event: &evtv1.Event_AssetBurnUpdated{AssetBurnUpdated: &evtv1.AssetBurnUpdatedEvent{State: state.Burn}}})
		changed, err := mutate(state.Burn, now, event.GetId())
		if err != nil {
			return nil, err
		}
		if changed {
			seq, err := c.EventPublisher.AppendAtFilter(ctx, agg.SubjectFor(event), event, filter, tail.Seq)
			if errors.Is(err, events.ErrConflict) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if err := c.waitForAssets(ctx, events.SubjectPosition(agg.SubjectFor(event), seq)); err != nil {
				return nil, err
			}
		}
		attachment := attachmentFromAsset(state.Creation.GetAsset())
		attachment.RoomId = state.RoomID
		return &BurnAttachmentResult{Attachment: attachment, Burn: proto.Clone(state.Burn).(*evtv1.AssetBurnState)}, nil
	}
	return nil, fmt.Errorf("attachment changed; retry the operation: %w", events.ErrConflict)
}

// OpenBurnAttachment durably reserves the recipient's only logical session
// before any bytes are served. Retrying the same live capability is safe.
func (c *AssetModel) OpenBurnAttachment(ctx context.Context, input BurnAttachmentInput) (*BurnAttachmentResult, error) {
	if len(input.SessionID) < 16 || len(input.SessionID) > 128 {
		return nil, invalidArgument("a random viewing session ID is required")
	}
	return c.mutateBurn(ctx, input, func(state *evtv1.AssetBurnState, now time.Time, _ string) (bool, error) {
		if state.GetPermanent() {
			return false, nil
		}
		if !slices.Contains(state.GetRecipientIds(), input.ActorID) {
			return false, ErrPermissionDenied
		}
		hash := burnSessionHash(input.SessionID)
		if view := burnView(state, input.ActorID); view != nil {
			if view.GetSessionHash() == hash && view.GetClosedAt() == nil && view.GetExpiresAt().AsTime().After(now) {
				return false, nil
			}
			return false, ErrPermissionDenied
		}
		if !state.GetUnopenedExpiresAt().AsTime().After(now) {
			return false, ErrPermissionDenied
		}
		state.Views = append(state.Views, &evtv1.AssetBurnView{UserId: input.ActorID, SessionHash: hash, ExpiresAt: timestamppb.New(now.Add(time.Duration(state.GetViewDurationMs()) * time.Millisecond))})
		return true, nil
	})
}

// CloseBurnAttachment ends the matching session without resetting its history.
func (c *AssetModel) CloseBurnAttachment(ctx context.Context, input BurnAttachmentInput) (*BurnAttachmentResult, error) {
	return c.mutateBurn(ctx, input, func(state *evtv1.AssetBurnState, now time.Time, _ string) (bool, error) {
		view := burnView(state, input.ActorID)
		if view == nil || view.GetSessionHash() != burnSessionHash(input.SessionID) {
			return false, ErrPermissionDenied
		}
		if view.GetClosedAt() != nil {
			return false, nil
		}
		// Closing after expiry cannot move the original recovery deadline later.
		closed := now
		if view.GetExpiresAt().AsTime().Before(closed) {
			closed = view.GetExpiresAt().AsTime()
		}
		view.ClosedAt = timestamppb.New(closed)
		return true, nil
	})
}

// RequestAttachmentPermanence toggles a request, not a viewing entitlement.
func (c *AssetModel) RequestAttachmentPermanence(ctx context.Context, input BurnAttachmentInput) (*BurnAttachmentResult, error) {
	return c.mutateBurn(ctx, input, func(state *evtv1.AssetBurnState, _ time.Time, _ string) (bool, error) {
		if !slices.Contains(state.GetRecipientIds(), input.ActorID) || input.ActorID == state.GetUserId() {
			return false, ErrPermissionDenied
		}
		if state.GetPermanent() {
			return false, nil
		}
		exists := slices.Contains(state.GetRequesterIds(), input.ActorID)
		if exists == input.Requested {
			return false, nil
		}
		if input.Requested {
			state.RequesterIds = append(state.RequesterIds, input.ActorID)
		} else {
			state.RequesterIds = slices.DeleteFunc(state.RequesterIds, func(id string) bool { return id == input.ActorID })
		}
		return true, nil
	})
}

// MakeAttachmentPermanent converts the whole file to normal message access.
// It records the first-use explanation across devices for this sender and room.
func (c *AssetModel) MakeAttachmentPermanent(ctx context.Context, input BurnAttachmentInput) (*BurnAttachmentResult, error) {
	if err := c.waitForAssetsCurrent(ctx); err != nil {
		return nil, err
	}
	return c.mutateBurn(ctx, input, func(state *evtv1.AssetBurnState, now time.Time, eventID string) (bool, error) {
		if input.ActorID != state.GetUserId() {
			return false, ErrPermissionDenied
		}
		if state.GetPermanent() {
			return false, nil
		}
		acknowledged := c.assets.Projection().permanenceAcknowledged(input.ActorID, input.RoomID)
		if !acknowledged && !input.Acknowledge {
			return false, invalidArgument("acknowledge the permanence explanation for this room")
		}
		state.PermanenceAcknowledged = true
		state.Permanent = true
		state.PermanentEventId = eventID
		state.UndoExpiresAt = timestamppb.New(now.Add(attachmentPermanenceUndoTTL))
		return true, nil
	})
}

// UndoAttachmentPermanence restores access restrictions without giving any
// recipient a new session or extending a prior expiry/recovery deadline.
func (c *AssetModel) UndoAttachmentPermanence(ctx context.Context, input BurnAttachmentInput) (*BurnAttachmentResult, error) {
	return c.mutateBurn(ctx, input, func(state *evtv1.AssetBurnState, now time.Time, _ string) (bool, error) {
		if input.ActorID != state.GetUserId() {
			return false, ErrPermissionDenied
		}
		if input.UndoToken == "" || input.UndoToken != state.GetPermanentEventId() {
			return false, ErrPermissionDenied
		}
		if !state.GetPermanent() {
			return false, nil
		}
		if state.GetUndoExpiresAt() == nil || !state.GetUndoExpiresAt().AsTime().After(now) {
			return false, ErrPermissionDenied
		}
		state.Permanent = false
		return true, nil
	})
}

// AuthorizeAssetBinary checks burn policy before cache lookup, transformation,
// S3 redirect, or object reads. A derivative inherits its parent's session.
// Existing authorization still checks current room and message access. The
// returned flag requires mediated delivery, including reversible permanence.
func (c *AssetModel) AuthorizeAssetBinary(ctx context.Context, assetID, userID, sessionID string, download bool) (bool, error) {
	state, err := c.FreshAssetState(ctx, assetID)
	if err != nil {
		return false, err
	}
	if state.Deleted || state.Creation == nil {
		return false, ErrNotFound
	}
	seen := map[string]bool{assetID: true}
	for parentID := state.Creation.GetParentAssetId(); parentID != ""; parentID = state.Creation.GetParentAssetId() {
		if seen[parentID] || len(seen) >= 16 {
			return false, ErrPermissionDenied
		}
		seen[parentID] = true
		state, err = c.FreshAssetState(ctx, parentID)
		if err != nil {
			return false, err
		}
		if state.Deleted || state.Creation == nil {
			return false, ErrNotFound
		}
	}
	burn := state.Burn
	if burn == nil {
		return false, nil
	}
	if burn.GetPermanent() {
		// A presigned S3 redirect cannot be recalled by Undo. Keep delivery
		// behind Chatto until this exact conversion's Undo period has ended.
		return burn.GetUndoExpiresAt() != nil && burn.GetUndoExpiresAt().AsTime().After(time.Now()), nil
	}
	if download || !burnRetained(burn, time.Now()) || !slices.Contains(burn.GetRecipientIds(), userID) {
		return true, ErrPermissionDenied
	}
	view := burnView(burn, userID)
	if sessionID == "" || view == nil || view.GetClosedAt() != nil || view.GetSessionHash() != burnSessionHash(sessionID) || !view.GetExpiresAt().AsTime().After(time.Now()) {
		return true, ErrPermissionDenied
	}
	return true, nil
}

// expireBurnAttachment commits revocation on the same OCC lane as permanence.
// Durable asset cleanup then removes bytes and all generated derivatives.
func (s *AssetModel) expireBurnAttachment(ctx context.Context, assetID string, now time.Time) error {
	agg := evtstream.AssetAggregate(assetID)
	for attempt := 0; attempt < maxAssetMutationAttempts; attempt++ {
		tail, err := s.EventPublisher.LastSubjectPosition(ctx, agg.AllEventsFilter())
		if err != nil {
			return err
		}
		if !tail.IsZero() {
			if err := s.waitForAssets(ctx, tail); err != nil {
				return err
			}
		}
		state := s.AssetState(assetID)
		if state.Deleted || state.Burn == nil || state.Burn.GetPermanent() || burnRetained(state.Burn, now) {
			return nil
		}
		event := newEvent(SystemActorID, &evtv1.Event{Event: &evtv1.Event_AssetDeleted{AssetDeleted: &evtv1.AssetDeletedEvent{AssetId: assetID}}})
		seq, err := s.EventPublisher.AppendAtFilter(ctx, agg.SubjectFor(event), event, agg.AllEventsFilter(), tail.Seq)
		if errors.Is(err, events.ErrConflict) {
			continue
		}
		if err != nil {
			return err
		}
		return s.waitForAssets(ctx, events.SubjectPosition(agg.SubjectFor(event), seq))
	}
	return events.ErrConflict
}

func (s *AssetModel) runBurnExpiry(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		for _, id := range s.assets.Projection().orphanDerivativeAssetIDs() {
			if err := s.DeleteAsset(ctx, SystemActorID, id); err != nil && ctx.Err() == nil {
				s.logger.Warn("Failed to expire orphaned attachment derivative", "asset_id", id, "error", err)
			}
		}
		for _, id := range s.assets.Projection().expiredBurnAssetIDs(time.Now()) {
			if err := s.expireBurnAttachment(ctx, id, time.Now()); err != nil && ctx.Err() == nil {
				s.logger.Warn("Failed to expire view-once attachment", "asset_id", id, "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
