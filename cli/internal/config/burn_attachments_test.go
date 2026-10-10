package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBurnAttachmentConfigDefaultsAndTOML(t *testing.T) {
	unopened, recovery, view := (BurnAttachmentsConfig{}).Lifetimes()
	require.Equal(t, 24*time.Hour, unopened)
	require.Equal(t, time.Hour, recovery)
	require.Equal(t, 5*time.Minute, view)
	path := filepath.Join(t.TempDir(), "chatto.toml")
	require.NoError(t, os.WriteFile(path, []byte("[core.assets.burn]\nunopened_ttl = '2h'\nrecovery_ttl = '30m'\nview_ttl = '90s'\n"), 0600))
	// ReadConfig also validates required deployment secrets; check decoding through
	// the normal reader with test deployment values rather than bypassing it.
	t.Setenv("CHATTO_WEBSERVER_COOKIE_SIGNING_SECRET", strings.Repeat("01", 32))
	t.Setenv("CHATTO_WEBSERVER_COOKIE_ENCRYPTION_SECRET", strings.Repeat("02", 16))
	t.Setenv("CHATTO_CORE_SECRET_KEY", strings.Repeat("03", 32))
	t.Setenv("CHATTO_CORE_ASSETS_SIGNING_SECRET", strings.Repeat("04", 32))
	t.Setenv("CHATTO_WEBSERVER_PORT", "4000")
	cfg, err := ReadConfig(path)
	require.NoError(t, err)
	unopened, recovery, view = cfg.Core.Assets.Burn.Lifetimes()
	require.Equal(t, 2*time.Hour, unopened)
	require.Equal(t, 30*time.Minute, recovery)
	require.Equal(t, 90*time.Second, view)
	for _, invalid := range []Duration{Duration(-time.Second), Duration(time.Nanosecond)} {
		cfg.Core.Assets.Burn.ViewTTL = invalid
		require.ErrorContains(t, cfg.Validate(), "core.assets.burn")
	}
}
