package video

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/require"
	"hmans.de/chatto/internal/config"
	"hmans.de/chatto/internal/core"
	"hmans.de/chatto/internal/testutil"
)

func TestBurnAudioDurationProcessingWithoutTranscoding(t *testing.T) {
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}
	_, nc := testutil.StartNATS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := core.NewChattoCore(ctx, nc, config.CoreConfig{SkipSetupWizard: true, SecretKey: "test-core-secret", Assets: config.AssetsConfig{SigningSecret: "test-signing-secret"}})
	require.NoError(t, err)
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("core did not stop")
		}
	})
	require.NoError(t, c.WaitForBoot(ctx))
	owner, err := c.CreateUser(ctx, core.SystemActorID, "burn-audio", "Audio sender", "password123")
	require.NoError(t, err)
	room, err := c.CreateRoom(ctx, owner.Id, core.KindChannel, "", "burn-audio", "")
	require.NoError(t, err)
	_, err = c.JoinRoom(ctx, owner.Id, core.KindChannel, owner.Id, room.Id)
	require.NoError(t, err)
	asset, err := c.UploadAttachment(ctx, owner.Id, room.Id, "clip.wav", "audio/wav", bytes.NewReader(audioWAVFixture(2)))
	require.NoError(t, err)
	message, err := c.Messages().PostMessage(ctx, core.MessagePostInput{ActorID: owner.Id, RoomID: room.Id, AttachmentAssetIDs: []string{asset.Id}, BurnAttachmentAssetIDs: []string{asset.Id}})
	require.NoError(t, err)
	require.NotNil(t, c.GetAssetState(asset.Id).VideoManifest)
	service := &Service{core: c, ffprobePath: ffprobePath, logger: log.New(io.Discard), config: config.AssetProcessingConfig{TempDir: t.TempDir()}}
	// No ffmpeg path: this must probe and commit duration without transcoding.
	require.NoError(t, service.ProcessAsset(ctx, asset.Id, message.Event.Id))
	result := c.GetAssetState(asset.Id).VideoManifest.Succeeded
	require.NotNil(t, result)
	require.Equal(t, int64(2000), result.AudioDurationMs)
	require.Nil(t, result.Video)
	require.NoError(t, service.ProcessAsset(ctx, asset.Id, message.Event.Id)) // durable redelivery is safe
	before := time.Now()
	opened, err := c.OpenBurnAttachment(ctx, core.BurnAttachmentInput{ActorID: owner.Id, RoomID: room.Id, AssetID: asset.Id, SessionID: "audio-session-123456"})
	require.NoError(t, err)
	require.WithinDuration(t, before.Add(2*time.Second), opened.Burn.Views[0].ExpiresAt.AsTime(), 500*time.Millisecond)
}

// Two-byte PCM samples at 16kHz give ffprobe an exact, local duration fixture.
func audioWAVFixture(seconds uint32) []byte {
	size := seconds * 16000 * 2
	data := make([]byte, 44+size)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], 36+size)
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 16000)
	binary.LittleEndian.PutUint32(data[28:], 32000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], size)
	return data
}
