package addons

import (
	"context"
	"strings"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// BrowseProbeEnsurer wraps the probe repair that Silo runs while listing an
// item's versions so it leaves addon streams alone. Probing a stream opens
// its link, and a debrid "resolve" link asks the service to fetch the
// torrent: listing twelve versions would add twelve torrents and take as long
// as the slowest. The stream that is actually played is still probed, by the
// playback handler's own ensurer at playback start.
func BrowseProbeEnsurer(inner catalog.PlaybackProbeEnsurer) catalog.PlaybackProbeEnsurer {
	if inner == nil {
		return nil
	}
	return browseProbeEnsurer{inner: inner}
}

type browseProbeEnsurer struct {
	inner catalog.PlaybackProbeEnsurer
}

func (b browseProbeEnsurer) EnsureProbeOnly(ctx context.Context, file *models.MediaFile) (*models.MediaFile, error) {
	if file != nil && strings.HasPrefix(file.FilePath, streamPathPrefix) {
		return file, nil
	}
	return b.inner.EnsureProbeOnly(ctx, file)
}

func (b browseProbeEnsurer) EnsureCopySafetyCached(ctx context.Context, file *models.MediaFile) (*models.MediaFile, error) {
	if file != nil && strings.HasPrefix(file.FilePath, streamPathPrefix) {
		return file, nil
	}
	return b.inner.EnsureCopySafetyCached(ctx, file)
}
