package http_server

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"hmans.de/chatto/internal/assets"
	"hmans.de/chatto/internal/core"
)

// A fixed transform with independent authorization; no query parameter can
// expose the original or start a viewing session.
func (s *HTTPServer) serveBurnPreview(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("assetID")
	userID, authCtx, ok := s.resolveStableAssetViewerID(c, ctx, id, nil)
	if !ok {
		return
	}
	attachment, ok := s.resolveAttachmentWithPolicy(c, authCtx, id, userID, true)
	if !ok {
		return
	}
	key := core.ImageCacheKey(core.BurnPreviewCacheResource, id, 160, 160, "blur")
	data, _ := s.core.GetCachedResize(ctx, key)
	if data == nil {
		reader, _, err := s.core.GetAttachmentReader(ctx, attachment)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		if closer, ok := reader.(io.Closer); ok {
			defer closer.Close()
		}
		source, err := io.ReadAll(io.LimitReader(reader, assets.DefaultMaxUploadSize+1))
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		data, err = assets.TransformBurnPreview(source)
		if err != nil {
			c.Status(http.StatusUnprocessableEntity)
			return
		}
		if err := s.core.StoreCachedResize(ctx, key, data); err != nil {
			s.logger.Warn("Failed to cache burn preview", "asset_id", id)
		}
	}
	c.Header("Cache-Control", protectedAssetCacheControl)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "image/jpeg", data)
}
