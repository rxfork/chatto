package core

import (
	"fmt"
	"hmans.de/chatto/internal/pb/chatto/core/projection/v1"
	"slices"

	"google.golang.org/protobuf/proto"

	evtv1 "hmans.de/chatto/internal/pb/chatto/core/evt/v1"
)

var assetSnapshotContractID = snapshotContractID("v3", &projectionv1.AssetProjectionSnapshot{})

func (*AssetProjection) SnapshotContractID() string { return assetSnapshotContractID }

func (p *AssetProjection) Snapshot() ([]byte, error) {
	p.RLock()
	defer p.RUnlock()
	snapshot := &projectionv1.AssetProjectionSnapshot{ReplayGuard: snapshotReplayGuard(p.replayGuard)}
	for _, assetID := range sortedMapKeys(p.assetCreations) {
		snapshot.Creations = append(snapshot.Creations, proto.Clone(p.assetCreations[assetID]).(*evtv1.AssetCreatedEvent))
	}
	for _, parentID := range sortedMapKeys(p.assetChildren) {
		children := slices.Clone(p.assetChildren[parentID])
		slices.Sort(children)
		snapshot.Children = append(snapshot.Children, &projectionv1.AssetChildrenSnapshot{ParentAssetId: parentID, ChildAssetIds: children})
	}
	for _, assetID := range sortedMapKeys(p.videoManifests) {
		manifest := p.videoManifests[assetID]
		row := &projectionv1.AssetManifestSnapshot{AssetId: assetID}
		if manifest.Started != nil {
			row.Started = proto.Clone(manifest.Started).(*evtv1.AssetProcessingStartedEvent)
		}
		if manifest.Succeeded != nil {
			row.Succeeded = proto.Clone(manifest.Succeeded).(*evtv1.AssetProcessingSucceededEvent)
		}
		if manifest.Failed != nil {
			row.Failed = proto.Clone(manifest.Failed).(*evtv1.AssetProcessingFailedEvent)
		}
		snapshot.Manifests = append(snapshot.Manifests, row)
	}
	for _, assetID := range sortedMapKeys(p.deletedAssets) {
		snapshot.DeletedAssets = append(snapshot.DeletedAssets, &projectionv1.DeletedAssetSnapshot{AssetId: assetID, RoomId: p.deletedAssetRoom[assetID]})
	}
	for _, assetID := range sortedMapKeys(p.messageOwners) {
		owner := p.messageOwners[assetID]
		snapshot.MessageOwners = append(snapshot.MessageOwners, &projectionv1.AssetMessageOwnerSnapshot{
			AssetId:        assetID,
			RoomId:         owner.roomID,
			MessageEventId: owner.messageEventID,
			AuthorId:       owner.authorID,
		})
	}
	snapshot.PublicLinkPreviewAssetIds = sortedMapKeys(p.publicLinkPreviewAssets)
	for _, id := range sortedMapKeys(p.burnStates) {
		snapshot.BurnStates = append(snapshot.BurnStates, proto.Clone(p.burnStates[id]).(*evtv1.AssetBurnState))
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(snapshot)
}

func (p *AssetProjection) Restore(data []byte) error {
	snapshot := &projectionv1.AssetProjectionSnapshot{}
	if len(data) > 0 {
		if err := proto.Unmarshal(data, snapshot); err != nil {
			return fmt.Errorf("unmarshal asset snapshot: %w", err)
		}
	}
	guard, err := restoreReplayGuard(snapshot.GetReplayGuard())
	if err != nil {
		return fmt.Errorf("asset snapshot replay guard: %w", err)
	}
	creations := make(map[string]*evtv1.AssetCreatedEvent, len(snapshot.GetCreations()))
	for _, creation := range snapshot.GetCreations() {
		assetID := creation.GetAsset().GetId()
		if assetID == "" {
			return fmt.Errorf("asset snapshot has creation without asset ID")
		}
		if _, duplicate := creations[assetID]; duplicate {
			return fmt.Errorf("asset snapshot repeats asset %q", assetID)
		}
		creations[assetID] = proto.Clone(creation).(*evtv1.AssetCreatedEvent)
	}
	children := make(map[string][]string, len(snapshot.GetChildren()))
	for _, row := range snapshot.GetChildren() {
		if row.GetParentAssetId() == "" {
			return fmt.Errorf("asset snapshot has empty parent ID")
		}
		if _, duplicate := children[row.GetParentAssetId()]; duplicate {
			return fmt.Errorf("asset snapshot repeats parent %q", row.GetParentAssetId())
		}
		seen := make(map[string]struct{}, len(row.GetChildAssetIds()))
		for _, childID := range row.GetChildAssetIds() {
			if childID == "" {
				return fmt.Errorf("asset snapshot has empty child ID")
			}
			if _, duplicate := seen[childID]; duplicate {
				return fmt.Errorf("asset snapshot repeats child %q", childID)
			}
			seen[childID] = struct{}{}
		}
		children[row.GetParentAssetId()] = slices.Clone(row.GetChildAssetIds())
	}
	manifests := make(map[string]*VideoAttachmentManifest, len(snapshot.GetManifests()))
	for _, row := range snapshot.GetManifests() {
		if row.GetAssetId() == "" {
			return fmt.Errorf("asset snapshot has empty manifest asset ID")
		}
		if _, duplicate := manifests[row.GetAssetId()]; duplicate {
			return fmt.Errorf("asset snapshot repeats manifest %q", row.GetAssetId())
		}
		if row.GetSucceeded() != nil && row.GetFailed() != nil {
			return fmt.Errorf("asset snapshot manifest %q has two terminal outcomes", row.GetAssetId())
		}
		manifest := &VideoAttachmentManifest{}
		if row.GetStarted() != nil {
			manifest.Started = proto.Clone(row.GetStarted()).(*evtv1.AssetProcessingStartedEvent)
		}
		if row.GetSucceeded() != nil {
			manifest.Succeeded = proto.Clone(row.GetSucceeded()).(*evtv1.AssetProcessingSucceededEvent)
		}
		if row.GetFailed() != nil {
			manifest.Failed = proto.Clone(row.GetFailed()).(*evtv1.AssetProcessingFailedEvent)
		}
		manifests[row.GetAssetId()] = manifest
	}
	deleted := make(map[string]struct{}, len(snapshot.GetDeletedAssets()))
	deletedRooms := make(map[string]string)
	for _, row := range snapshot.GetDeletedAssets() {
		if row.GetAssetId() == "" {
			return fmt.Errorf("asset snapshot has empty deleted asset ID")
		}
		if _, duplicate := deleted[row.GetAssetId()]; duplicate {
			return fmt.Errorf("asset snapshot repeats deleted asset %q", row.GetAssetId())
		}
		if creations[row.GetAssetId()] != nil || manifests[row.GetAssetId()] != nil {
			return fmt.Errorf("asset snapshot retains deleted asset %q", row.GetAssetId())
		}
		deleted[row.GetAssetId()] = struct{}{}
		if row.GetRoomId() != "" {
			deletedRooms[row.GetAssetId()] = row.GetRoomId()
		}
	}
	messageOwners := make(map[string]assetMessageRef, len(snapshot.GetMessageOwners()))
	for _, row := range snapshot.GetMessageOwners() {
		if row.GetAssetId() == "" || row.GetRoomId() == "" || row.GetMessageEventId() == "" {
			return fmt.Errorf("asset snapshot has invalid message owner")
		}
		if _, duplicate := messageOwners[row.GetAssetId()]; duplicate {
			return fmt.Errorf("asset snapshot repeats message owner %q", row.GetAssetId())
		}
		messageOwners[row.GetAssetId()] = assetMessageRef{
			roomID:         row.GetRoomId(),
			messageEventID: row.GetMessageEventId(),
			authorID:       row.GetAuthorId(),
		}
	}
	publicLinkPreviewAssets := make(map[string]struct{}, len(snapshot.GetPublicLinkPreviewAssetIds()))
	for _, assetID := range snapshot.GetPublicLinkPreviewAssetIds() {
		if assetID == "" {
			return fmt.Errorf("asset snapshot has empty public link preview asset ID")
		}
		if _, duplicate := publicLinkPreviewAssets[assetID]; duplicate {
			return fmt.Errorf("asset snapshot repeats public link preview asset %q", assetID)
		}
		publicLinkPreviewAssets[assetID] = struct{}{}
	}
	burns := make(map[string]*evtv1.AssetBurnState, len(snapshot.GetBurnStates()))
	for _, burn := range snapshot.GetBurnStates() {
		if burn.GetAssetId() == "" || burn.GetRoomId() == "" || burn.GetUserId() == "" || burn.GetMessageEventId() == "" || burn.GetUnopenedExpiresAt() == nil || burn.GetUnopenedExpiresAt().CheckValid() != nil || burn.GetViewDurationMs() <= 0 || burn.GetRecoveryDurationMs() <= 0 {
			return fmt.Errorf("asset snapshot has invalid burn state")
		}
		audience := make(map[string]bool)
		for _, id := range burn.GetRecipientIds() {
			if id == "" || audience[id] {
				return fmt.Errorf("asset snapshot has invalid burn audience")
			}
			audience[id] = true
		}
		views := make(map[string]bool)
		for _, view := range burn.GetViews() {
			if !audience[view.GetUserId()] || views[view.GetUserId()] || len(view.GetSessionHash()) != 64 || view.GetExpiresAt() == nil || view.GetExpiresAt().CheckValid() != nil {
				return fmt.Errorf("asset snapshot has invalid burn session")
			}
			if view.GetClosedAt() != nil && (view.GetClosedAt().CheckValid() != nil || view.GetClosedAt().AsTime().After(view.GetExpiresAt().AsTime())) {
				return fmt.Errorf("asset snapshot has invalid burn close")
			}
			views[view.GetUserId()] = true
		}
		requests := make(map[string]bool)
		for _, id := range burn.GetRequesterIds() {
			if !audience[id] || id == burn.GetUserId() || requests[id] {
				return fmt.Errorf("asset snapshot has invalid permanence request")
			}
			requests[id] = true
		}
		if burn.GetPermanent() && (burn.GetPermanentEventId() == "" || burn.GetUndoExpiresAt() == nil || burn.GetUndoExpiresAt().CheckValid() != nil || !burn.GetPermanenceAcknowledged()) {
			return fmt.Errorf("asset snapshot has invalid permanence state")
		}
		if _, duplicate := burns[burn.GetAssetId()]; duplicate {
			return fmt.Errorf("asset snapshot repeats burn state")
		}
		burns[burn.GetAssetId()] = proto.Clone(burn).(*evtv1.AssetBurnState)
	}
	p.Lock()
	p.burnStates = burns
	p.assetCreations, p.assetChildren, p.videoManifests, p.deletedAssets, p.deletedAssetRoom, p.messageOwners, p.publicLinkPreviewAssets, p.replayGuard = creations, children, manifests, deleted, deletedRooms, messageOwners, publicLinkPreviewAssets, guard
	p.Unlock()
	return nil
}
