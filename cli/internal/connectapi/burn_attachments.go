package connectapi

import (
	"context"
	"net/url"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
	"hmans.de/chatto/internal/core"
	apiv1 "hmans.de/chatto/internal/pb/chatto/api/v1"
)

func apiBurnAttachment(api *API, assetID, viewerID string) *apiv1.BurnAttachment {
	view := api.core.BurnAttachmentMetadata(assetID, viewerID)
	if view == nil {
		return nil
	}
	statuses := map[string]apiv1.BurnAttachmentViewerStatus{
		"available":  apiv1.BurnAttachmentViewerStatus_BURN_ATTACHMENT_VIEWER_STATUS_AVAILABLE,
		"viewing":    apiv1.BurnAttachmentViewerStatus_BURN_ATTACHMENT_VIEWER_STATUS_VIEWING,
		"burned":     apiv1.BurnAttachmentViewerStatus_BURN_ATTACHMENT_VIEWER_STATUS_BURNED,
		"expired":    apiv1.BurnAttachmentViewerStatus_BURN_ATTACHMENT_VIEWER_STATUS_EXPIRED,
		"ineligible": apiv1.BurnAttachmentViewerStatus_BURN_ATTACHMENT_VIEWER_STATUS_INELIGIBLE,
		"purged":     apiv1.BurnAttachmentViewerStatus_BURN_ATTACHMENT_VIEWER_STATUS_PURGED,
		"permanent":  apiv1.BurnAttachmentViewerStatus_BURN_ATTACHMENT_VIEWER_STATUS_PERMANENT,
	}
	return &apiv1.BurnAttachment{
		ViewerStatus: statuses[view.Status], UnopenedExpiresAt: view.UnopenedExpiresAt, DeleteAt: view.DeleteAt,
		ViewExpiresAt: view.ViewExpiresAt, CanMakePermanent: view.CanMakePermanent,
		CanRequestPermanent: view.CanRequestPermanent, PermanenceRequested: view.PermanenceRequested,
		RequesterIds: view.RequesterIDs, RequiresPermanenceConfirmation: view.RequiresPermanenceConfirmation,
	}
}

func burnSessionURL(value *apiv1.MessageAssetUrl, sessionID string, expiresAt *timestamppb.Timestamp) *apiv1.MessageAssetUrl {
	if value == nil {
		return nil
	}
	parsed, err := url.Parse(value.GetUrl())
	if err != nil {
		return nil
	}
	query := parsed.Query()
	query.Set("burn_session", sessionID)
	parsed.RawQuery = query.Encode()
	value.Url = parsed.String()
	if expiresAt != nil && (value.ExpiresAt == nil || expiresAt.AsTime().Before(value.ExpiresAt.AsTime())) {
		value.ExpiresAt = expiresAt
	}
	return value
}

func (s *assetService) OpenBurnAttachment(ctx context.Context, req *connect.Request[apiv1.OpenBurnAttachmentRequest]) (*connect.Response[apiv1.OpenBurnAttachmentResponse], error) {
	caller, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.api.core.OpenBurnAttachment(ctx, core.BurnAttachmentInput{ActorID: caller.UserID, RoomID: req.Msg.RoomId, AssetID: req.Msg.AssetId, SessionID: req.Msg.SessionId})
	if err != nil {
		return nil, err
	}
	asset := apiAsset(ctx, s.api, result.Attachment, caller.UserID, assetThumbnailOptions(nil))
	var expiresAt *timestamppb.Timestamp
	if !result.Burn.GetPermanent() {
		for _, view := range result.Burn.GetViews() {
			if view.GetUserId() == caller.UserID {
				expiresAt = view.GetExpiresAt()
			}
		}
		asset.AssetUrl = burnSessionURL(s.api.assetURLView(ctx, s.api.core.GetStableAttachmentAssetURL(req.Msg.AssetId, caller.UserID)), req.Msg.SessionId, expiresAt)
		asset.ThumbnailAssetUrl = burnSessionURL(s.api.assetURLView(ctx, s.api.core.GetStableTransformedAttachmentAssetURL(req.Msg.AssetId, caller.UserID, 2048, 2048, "contain")), req.Msg.SessionId, expiresAt)
		asset.VideoProcessing = apiVideoProcessing(ctx, s.api, caller.UserID, result.Attachment)
		if video := asset.VideoProcessing; video != nil {
			video.ThumbnailAssetUrl = burnSessionURL(video.ThumbnailAssetUrl, req.Msg.SessionId, expiresAt)
			for _, variant := range video.Variants {
				variant.AssetUrl = burnSessionURL(variant.AssetUrl, req.Msg.SessionId, expiresAt)
			}
			if video.Hls != nil {
				video.Hls.MasterPlaylistUrl = burnSessionURL(video.Hls.MasterPlaylistUrl, req.Msg.SessionId, expiresAt)
			}
		}
	}
	return connect.NewResponse(&apiv1.OpenBurnAttachmentResponse{Asset: asset, ViewExpiresAt: expiresAt}), nil
}

func (s *assetService) CloseBurnAttachment(ctx context.Context, req *connect.Request[apiv1.CloseBurnAttachmentRequest]) (*connect.Response[apiv1.CloseBurnAttachmentResponse], error) {
	caller, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.api.core.CloseBurnAttachment(ctx, core.BurnAttachmentInput{ActorID: caller.UserID, RoomID: req.Msg.RoomId, AssetID: req.Msg.AssetId, SessionID: req.Msg.SessionId})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&apiv1.CloseBurnAttachmentResponse{Asset: apiAsset(ctx, s.api, result.Attachment, caller.UserID, assetThumbnailOptions(nil))}), nil
}

func (s *assetService) RequestAttachmentPermanence(ctx context.Context, req *connect.Request[apiv1.RequestAttachmentPermanenceRequest]) (*connect.Response[apiv1.RequestAttachmentPermanenceResponse], error) {
	caller, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.api.core.RequestAttachmentPermanence(ctx, core.BurnAttachmentInput{ActorID: caller.UserID, RoomID: req.Msg.RoomId, AssetID: req.Msg.AssetId, Requested: req.Msg.Requested})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&apiv1.RequestAttachmentPermanenceResponse{Asset: apiAsset(ctx, s.api, result.Attachment, caller.UserID, assetThumbnailOptions(nil))}), nil
}

func (s *assetService) MakeAttachmentPermanent(ctx context.Context, req *connect.Request[apiv1.MakeAttachmentPermanentRequest]) (*connect.Response[apiv1.MakeAttachmentPermanentResponse], error) {
	caller, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.api.core.MakeAttachmentPermanent(ctx, core.BurnAttachmentInput{ActorID: caller.UserID, RoomID: req.Msg.RoomId, AssetID: req.Msg.AssetId, Acknowledge: req.Msg.Acknowledge})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&apiv1.MakeAttachmentPermanentResponse{Asset: apiAsset(ctx, s.api, result.Attachment, caller.UserID, assetThumbnailOptions(nil)), UndoToken: result.Burn.GetPermanentEventId(), UndoExpiresAt: result.Burn.GetUndoExpiresAt()}), nil
}

func (s *assetService) UndoAttachmentPermanence(ctx context.Context, req *connect.Request[apiv1.UndoAttachmentPermanenceRequest]) (*connect.Response[apiv1.UndoAttachmentPermanenceResponse], error) {
	caller, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.api.core.UndoAttachmentPermanence(ctx, core.BurnAttachmentInput{ActorID: caller.UserID, RoomID: req.Msg.RoomId, AssetID: req.Msg.AssetId, UndoToken: req.Msg.UndoToken})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&apiv1.UndoAttachmentPermanenceResponse{Asset: apiAsset(ctx, s.api, result.Attachment, caller.UserID, assetThumbnailOptions(nil))}), nil
}
