package playback

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/httpstream"
)

// Quillo: media files whose bytes live at a remote URL, such as addon streams
// (FORK_NOTES.md). A registered resolver maps such a file's path to the URL
// and the request headers it needs; direct play then proxies byte ranges
// from it. ffmpeg and ffprobe reach it through procgroup's argument rewrite.
// The client only ever talks to this server, as the Silo apps require.

// RemoteSource is where a remote media file's bytes are.
type RemoteSource struct {
	URL     string
	Headers map[string]string
}

// RemoteSourceResolver reports the remote source for a media file path, or
// ok=false for an ordinary local file.
type RemoteSourceResolver func(ctx context.Context, filePath string) (src RemoteSource, ok bool, err error)

// RemoteFailureReporter is told when a remote source answered with an error,
// so its owner can stop offering it.
type RemoteFailureReporter func(ctx context.Context, filePath string, status int)

var (
	remoteMu       sync.RWMutex
	remoteResolver RemoteSourceResolver
	remoteFailed   RemoteFailureReporter
)

// RegisterRemoteFailureReporter installs the callback for failing sources.
func RegisterRemoteFailureReporter(f RemoteFailureReporter) {
	remoteMu.Lock()
	defer remoteMu.Unlock()
	remoteFailed = f
}

// RegisterRemoteSourceResolver installs the resolver used by direct play and
// remux.
func RegisterRemoteSourceResolver(r RemoteSourceResolver) {
	remoteMu.Lock()
	defer remoteMu.Unlock()
	remoteResolver = r
}

// IsRemoteSource reports whether filePath is a remote source rather than a
// file on disk, for checks that would otherwise stat it.
func IsRemoteSource(ctx context.Context, filePath string) bool {
	_, ok, err := resolveRemoteSource(ctx, filePath)
	return ok && err == nil
}

func resolveRemoteSource(ctx context.Context, filePath string) (RemoteSource, bool, error) {
	remoteMu.RLock()
	r := remoteResolver
	remoteMu.RUnlock()
	if r == nil {
		return RemoteSource{}, false, nil
	}
	return r(ctx, filePath)
}

var remoteClient = &http.Client{
	// No overall timeout: a body streams for the length of a film. Each
	// request still ends when the client goes away.
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   8,
	},
}

// remoteRequestHeaders are copied from the client's request to the source so
// ranges and validators behave as they would against a local file.
var remoteRequestHeaders = []string{"Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since"}

// remoteResponseHeaders are copied back to the client.
var remoteResponseHeaders = []string{"Content-Range", "Content-Length", "Accept-Ranges", "ETag", "Last-Modified"}

// ServeRemoteSource proxies a byte-range request for the media file at
// filePath to its remote source.
func ServeRemoteSource(w http.ResponseWriter, r *http.Request, filePath string, src RemoteSource, contentType string) error {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, src.URL, nil)
	if err != nil {
		http.Error(w, "stream unavailable", http.StatusBadGateway)
		return err
	}
	for k, v := range src.Headers {
		req.Header.Set(k, v)
	}
	for _, h := range remoteRequestHeaders {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	resp, err := remoteClient.Do(req)
	if err != nil {
		if errors.Is(r.Context().Err(), context.Canceled) {
			return nil
		}
		http.Error(w, "stream unavailable", http.StatusBadGateway)
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK, resp.StatusCode == http.StatusPartialContent,
		resp.StatusCode == http.StatusNotModified, resp.StatusCode == http.StatusRequestedRangeNotSatisfiable,
		resp.StatusCode == http.StatusPreconditionFailed:
	default:
		// A dead link (404, 403, an expired token) is a gateway failure, so
		// the client replans instead of treating the media as broken.
		remoteMu.RLock()
		report := remoteFailed
		remoteMu.RUnlock()
		if report != nil && resp.StatusCode >= 400 {
			report(context.WithoutCancel(r.Context()), filePath, resp.StatusCode)
		}
		http.Error(w, "stream unavailable", http.StatusBadGateway)
		return errors.New("remote source answered " + resp.Status)
	}
	for _, h := range remoteResponseHeaders {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if w.Header().Get("Accept-Ranges") == "" {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return nil
	}
	streamWriter := httpstream.NewRollingDeadlineWriter(w)
	_, err = io.Copy(streamWriter, resp.Body)
	if err != nil && r.Context().Err() == nil {
		slog.WarnContext(r.Context(), "remote direct play ended early", "component", "playback", "error", err)
	}
	return nil
}
