package scanner

import (
	"context"
	"sync"

	"github.com/Silo-Server/silo-server/internal/models"
)

// Quillo: addon streams are probed over HTTP and recorded like scanned files
// (FORK_NOTES.md).

// ApplyProbe fills a media file's container, codecs, tracks, chapters,
// duration and bitrate from a probe, as a scan does.
func ApplyProbe(mf *models.MediaFile, probe *ProbeData) {
	applyProbeData(mf, probe, "quillo_stream")
}

// ProbeURLResolver maps a media file path to the URL ffprobe should read, or
// ok=false for a file on disk.
type ProbeURLResolver func(ctx context.Context, filePath string) (url string, ok bool)

var (
	probeMu       sync.RWMutex
	probeResolver ProbeURLResolver
)

// RegisterProbeURLResolver lets probes of remote media files (such as the
// playback probe repair) read the file's real URL.
func RegisterProbeURLResolver(r ProbeURLResolver) {
	probeMu.Lock()
	defer probeMu.Unlock()
	probeResolver = r
}

func probeInput(ctx context.Context, filePath string) string {
	probeMu.RLock()
	r := probeResolver
	probeMu.RUnlock()
	if r == nil {
		return filePath
	}
	if url, ok := r(ctx, filePath); ok {
		return url
	}
	return filePath
}
