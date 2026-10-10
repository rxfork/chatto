package http_server

import (
	"bytes"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"hmans.de/chatto/internal/core"
	apiv1 "hmans.de/chatto/internal/pb/chatto/api/v1"
	"hmans.de/chatto/internal/pb/chatto/api/v1/apiv1connect"
	evtv1 "hmans.de/chatto/internal/pb/chatto/core/evt/v1"
)

func TestBurnAttachmentMetadataAndHTTPRevocation(t *testing.T) {
	for _, useS3 := range []bool{false, true} {
		name := "nats"
		if useS3 {
			name = "s3"
		}
		t.Run(name, func(t *testing.T) {
			env := setupAssetTestServerWithConfig(t, useS3)
			env.client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
			owner, err := env.core.CreateUser(env.ctx, core.SystemActorID, "burn-http", "Burn sender", "password123")
			require.NoError(t, err)
			room, err := env.core.CreateRoom(env.ctx, owner.Id, core.KindChannel, "", "burn-http", "")
			require.NoError(t, err)
			_, err = env.core.JoinRoom(env.ctx, owner.Id, core.KindChannel, owner.Id, room.Id)
			require.NoError(t, err)
			env.login(t, owner.Login, "password123")
			messageID, attachment := env.postAssetMessageWithAttachmentContentType(t, room.Id, "", createAssetTestPNG(t, 48, 48), "secret.png", "image/png", true)
			require.NotNil(t, attachment.Burn)
			require.Nil(t, attachment.AssetUrl)
			require.Nil(t, attachment.ThumbnailAssetUrl)
			client := apiv1connect.NewAssetServiceClient(env.client, env.server.URL+connectAPIPrefix)
			metadata, err := client.GetAsset(env.ctx, connect.NewRequest(&apiv1.GetAssetRequest{RoomId: room.Id, AssetId: attachment.Id}))
			require.NoError(t, err)
			require.Nil(t, metadata.Msg.Asset.AssetUrl)
			require.Equal(t, apiv1.BurnAttachmentViewerStatus_BURN_ATTACHMENT_VIEWER_STATUS_AVAILABLE, metadata.Msg.Asset.Burn.ViewerStatus)
			// Even an ordinary signed ticket minted before/during the burn is not a session.
			ordinaryURL := env.core.GetStableAttachmentAssetURL(attachment.Id, owner.Id)
			assertStatus := func(path string, want int) {
				t.Helper()
				resp, err := env.client.Get(env.url(path))
				require.NoError(t, err)
				defer resp.Body.Close()
				_, _ = io.Copy(io.Discard, resp.Body)
				require.Equal(t, want, resp.StatusCode)
				if want == http.StatusOK {
					require.Empty(t, resp.Header.Get("Location"))
					require.Contains(t, resp.Header.Get("Cache-Control"), "no-store")
				}
			}
			assertStatus(ordinaryURL.URL, http.StatusForbidden)
			require.NotNil(t, metadata.Msg.Asset.Burn.PreviewAssetUrl)
			previewURL := metadata.Msg.Asset.Burn.PreviewAssetUrl.Url
			resp, err := env.client.Get(env.url(previewURL + "&width=4096&fit=original"))
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, "private, no-store", resp.Header.Get("Cache-Control"))
			previewBytes, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			resp.Body.Close()
			preview, err := jpeg.Decode(bytes.NewReader(previewBytes))
			require.NoError(t, err)
			require.Equal(t, image.Rect(0, 0, 160, 160), preview.Bounds())
			require.Empty(t, env.core.GetAssetState(attachment.Id).Burn.Views)
			assertStatus(ordinaryURL.URL, http.StatusForbidden)
			opened, err := client.OpenBurnAttachment(env.ctx, connect.NewRequest(&apiv1.OpenBurnAttachmentRequest{RoomId: room.Id, AssetId: attachment.Id, SessionId: "http-session-1234567"}))
			require.NoError(t, err)
			require.NotNil(t, opened.Msg.ViewExpiresAt)
			original, thumbnail := opened.Msg.Asset.AssetUrl.Url, opened.Msg.Asset.ThumbnailAssetUrl.Url
			require.Contains(t, original, "burn_session=")
			assertStatus(original, http.StatusOK)
			assertStatus(thumbnail, http.StatusOK) // populate transformation cache
			assertStatus(original+"&download=1", http.StatusForbidden)
			_, err = client.CloseBurnAttachment(env.ctx, connect.NewRequest(&apiv1.CloseBurnAttachmentRequest{RoomId: room.Id, AssetId: attachment.Id, SessionId: "http-session-1234567"}))
			require.NoError(t, err)
			assertStatus(original, http.StatusForbidden)
			assertStatus(thumbnail, http.StatusForbidden) // cache cannot bypass a consumed session
			permanent, err := client.MakeAttachmentPermanent(env.ctx, connect.NewRequest(&apiv1.MakeAttachmentPermanentRequest{RoomId: room.Id, AssetId: attachment.Id, Acknowledge: true}))
			require.NoError(t, err)
			assertStatus(previewURL, http.StatusForbidden)                // cached preview rechecks permanent state
			assertStatus(permanent.Msg.Asset.AssetUrl.Url, http.StatusOK) // no escaping S3 grant during Undo
			assertStatus(permanent.Msg.Asset.AssetUrl.Url+"&download=1", http.StatusOK)
			_, err = client.UndoAttachmentPermanence(env.ctx, connect.NewRequest(&apiv1.UndoAttachmentPermanenceRequest{RoomId: room.Id, AssetId: attachment.Id, UndoToken: permanent.Msg.UndoToken}))
			require.NoError(t, err)
			assertStatus(permanent.Msg.Asset.AssetUrl.Url, http.StatusForbidden)
			assertStatus(original, http.StatusForbidden)
			assertStatus(previewURL, http.StatusOK) // Undo restores retained preview access
			env.deleteAssetMessage(t, room.Id, messageID)
			assertStatus(previewURL, http.StatusNotFound) // cached bytes cannot bypass deletion
		})
	}
}

func TestBurnHLSChildPathsPreserveOnlyViewingCapability(t *testing.T) {
	path := hlsChildSessionPath("asset", "access-ticket", "segment.ts", "session capability")
	u, err := url.Parse(path)
	require.NoError(t, err)
	require.Equal(t, "session capability", u.Query().Get("burn_session"))
	require.Equal(t, "access-ticket", u.Query().Get("access"))
}

func TestBurnRealtimeInvalidationDoesNotExposeViewingHistory(t *testing.T) {
	event := &evtv1.Event{Id: "fact", ActorId: "reader", Event: &evtv1.Event_AssetBurnUpdated{AssetBurnUpdated: &evtv1.AssetBurnUpdatedEvent{State: &evtv1.AssetBurnState{
		AssetId: "asset", RoomId: "room", MessageEventId: "message", RecipientIds: []string{"private-recipient"},
		Views: []*evtv1.AssetBurnView{{UserId: "reader", SessionHash: "secret-hash"}}, RequesterIds: []string{"private-requester"},
	}}}}
	public := projectRealtimeEvent("viewer", event)
	require.NotNil(t, public)
	require.Empty(t, public.GetActorId())
	require.Equal(t, "asset", public.GetAttachmentChanged().AssetId)
	data, err := protojson.Marshal(public)
	require.NoError(t, err)
	for _, private := range []string{"reader", "private-recipient", "secret-hash", "private-requester"} {
		require.False(t, strings.Contains(string(data), private))
	}
}
