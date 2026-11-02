package addons

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// ffmpeg and ffprobe never see an addon's URL. They read a stream through
// this server instead, at a loopback URL that only this process knows:
//
//	http://127.0.0.1:<PORT>/api/quillo/internal/stream/<token>/<hash>
//
// so a debrid token in an addon link never lands in a command line, a log or
// an error, request headers an addon asked for are still sent, and a failing
// link is noticed in one place. The route answers loopback clients holding
// the per-process token only.

const loopbackRoutePrefix = "/api/quillo/internal/stream/"

func newLoopbackToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("quillo: no randomness for the stream token: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func listenPort() string {
	if p := strings.TrimSpace(os.Getenv("PORT")); p != "" {
		return p
	}
	return "8080"
}

// streamHash is the <hash> part of a placeholder path
// "/quillo-stream/<hash>/<label>.<ext>".
func streamHash(filePath string) (string, bool) {
	rest, ok := strings.CutPrefix(filePath, streamPathPrefix)
	if !ok {
		return "", false
	}
	hash, _, ok := strings.Cut(rest, "/")
	return hash, ok && hash != ""
}

// rewriteArg is the procgroup rewriter: a placeholder path argument becomes
// its loopback URL.
func (s *Service) rewriteArg(ctx context.Context, arg string) (string, bool) {
	hash, ok := streamHash(arg)
	if !ok {
		return "", false
	}
	if _, found, err := s.remote.resolve(ctx, arg); err != nil || !found {
		return "", false
	}
	return "http://127.0.0.1:" + listenPort() + loopbackRoutePrefix + s.loopbackToken + "/" + hash, true
}

// InternalStream serves a stream's bytes to this server's own ffmpeg and
// ffprobe.
func (h *Handler) InternalStream(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if subtle.ConstantTimeCompare([]byte(token), []byte(h.svc.loopbackToken)) != 1 || !fromLoopback(r) {
		http.NotFound(w, r)
		return
	}
	var filePath string
	err := h.svc.store.db.QueryRow(r.Context(), `SELECT file_path FROM quillo_streams
		WHERE file_path LIKE $1 LIMIT 1`, streamPathPrefix+chi.URLParam(r, "hash")+"/%").Scan(&filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	src, ok, err := h.svc.remote.resolve(r.Context(), filePath)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	_ = playback.ServeRemoteSource(w, r, filePath, src, playback.MimeFromExtension(filePath))
}

// fromLoopback reports whether the connection itself came from this machine.
// It reads the transport peer, which forwarding headers can't change; Silo's
// client-IP middleware replaces RemoteAddr with the resolved client address.
func fromLoopback(r *http.Request) bool {
	peer := clientip.PeerFromContext(r.Context())
	if peer == "" {
		peer = r.RemoteAddr
	}
	host := peer
	if h, _, err := net.SplitHostPort(peer); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// streamFailed hides a stream whose link answered with an error, so the next
// play picks another version, and lets the next detail view ask the addons
// again. The link itself is never logged.
func (s *Service) streamFailed(ctx context.Context, filePath string, status int) {
	if !strings.HasPrefix(filePath, streamPathPrefix) {
		return
	}
	var key string
	_ = s.store.db.QueryRow(ctx, `SELECT title_key FROM quillo_streams WHERE file_path = $1`, filePath).Scan(&key)
	if _, err := s.store.db.Exec(ctx, `UPDATE media_files SET missing_since = now()
		WHERE file_path = $1 AND missing_since IS NULL`, filePath); err != nil {
		slog.WarnContext(ctx, "quillo streams: hiding a failed stream", "error", err)
	}
	if key != "" {
		_, _ = s.store.db.Exec(ctx, `DELETE FROM quillo_stream_sets WHERE title_key = $1`, key)
		s.streams.mu.Lock()
		delete(s.streams.fresh, key)
		s.streams.mu.Unlock()
	}
	s.remote.forget(filePath)
	slog.InfoContext(ctx, "quillo streams: a stream's link failed; hiding it", "title", key, "status", status)
}
