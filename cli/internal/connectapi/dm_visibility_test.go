package connectapi

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"hmans.de/chatto/internal/core"
	apiv1 "hmans.de/chatto/internal/pb/chatto/api/v1"
)

func TestMyAccountDMVisibilityIsPrivateAndReadYourWrites(t *testing.T) {
	env := newConnectAPITestEnv(t)
	peer, err := env.core.CreateUser(env.ctx, core.SystemActorID, "visibility-api-peer", "Peer", "password123")
	require.NoError(t, err)
	dm, _, err := env.core.FindOrCreateDM(env.ctx, env.viewer.Id, []string{peer.Id})
	require.NoError(t, err)
	request := connect.NewRequest(&apiv1.SetDMVisibilityRequest{RoomId: dm.Id, Hidden: true})
	_, err = env.account.SetDMVisibility(env.ctx, request)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	ctx := withCaller(env.ctx, env.viewer)
	result, err := env.account.SetDMVisibility(ctx, request)
	require.NoError(t, err)
	require.Equal(t, []string{dm.Id}, result.Msg.GetSettings().GetHiddenDmRoomIds())
	settings, err := env.account.GetSettings(ctx, connect.NewRequest(&apiv1.GetSettingsRequest{}))
	require.NoError(t, err)
	require.Equal(t, []string{dm.Id}, settings.Msg.GetSettings().GetHiddenDmRoomIds())
	viewer, err := env.viewerService.GetViewer(ctx, connect.NewRequest(&apiv1.GetViewerRequest{}))
	require.NoError(t, err)
	require.Equal(t, []string{dm.Id}, viewer.Msg.GetUser().GetSettings().GetHiddenDmRoomIds())
	peerSettings, err := env.account.GetSettings(withCaller(env.ctx, peer), connect.NewRequest(&apiv1.GetSettingsRequest{}))
	require.NoError(t, err)
	require.Empty(t, peerSettings.Msg.GetSettings().GetHiddenDmRoomIds())
	// Patching unrelated display settings must preserve private visibility.
	share := false
	patched, err := env.account.UpdateSettings(ctx, connect.NewRequest(&apiv1.UpdateSettingsRequest{ShareTimezone: &share}))
	require.NoError(t, err)
	require.Equal(t, []string{dm.Id}, patched.Msg.GetSettings().GetHiddenDmRoomIds())
	restored, err := env.account.SetDMVisibility(ctx, connect.NewRequest(&apiv1.SetDMVisibilityRequest{RoomId: dm.Id}))
	require.NoError(t, err)
	require.Empty(t, restored.Msg.GetSettings().GetHiddenDmRoomIds())
}
