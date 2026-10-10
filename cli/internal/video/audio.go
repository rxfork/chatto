package video

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"hmans.de/chatto/internal/core"
)

// Audio jobs record duration only; the original remains the playback source.
func (s *Service) processAudioDuration(parentCtx context.Context, req processRequest) error {
	ctx, cancel := context.WithTimeout(parentCtx, videoProcessingAttemptTimeout)
	defer cancel()
	fail := func(err error) error {
		return s.finalizeProcessingFailure(parentCtx, req, nil, processingAttemptFailure(parentCtx, ctx, err))
	}
	tmpDir, err := os.MkdirTemp(s.config.TempDir, "chatto-audio-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	inputPath := filepath.Join(tmpDir, "input")
	if err := s.downloadAttachment(ctx, req.Attachment, inputPath); err != nil {
		return fail(err)
	}
	durationMs, err := s.probeAudioDuration(ctx, inputPath, req.ContentType)
	if err != nil {
		return fail(err)
	}
	finalizeCtx, finalizeCancel := videoProcessingFinalizationContext(parentCtx)
	defer finalizeCancel()
	return s.core.RecordAssetAudioDuration(finalizeCtx, core.SystemActorID, req.RoomID, req.MessageEventID, req.AssetID, durationMs)
}

func (s *Service) probeAudioDuration(ctx context.Context, inputPath, contentType string) (int64, error) {
	result, err := s.probe(ctx, inputPath, contentType)
	if err != nil {
		return 0, err
	}
	if result.AudioCodec == "" || result.DurationMs <= 0 || result.DurationMs > math.MaxInt64/int64(time.Millisecond) {
		return 0, fmt.Errorf("audio has no valid finite duration")
	}
	return result.DurationMs, nil
}
