package addons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

const (
	// streamPathPrefix marks a media file whose bytes are an addon stream.
	streamPathPrefix = "/quillo-stream/"
	// A title's streams are asked for again after this long. Debrid links
	// usually stay valid for hours, and a fresh list picks up new releases.
	streamSetFresh = 30 * time.Minute
	// Streams kept per title, best first.
	maxStreamsPerTitle = 12
	// Probes run this many at a time; the viewer waits for them up to
	// probeDeadline.
	probeParallel = 6
	probeDeadline = 12 * time.Second
	// How long a detail page or playback start waits for addons at most.
	streamsDeadline = 10 * time.Second
)

// titleRef is what Quillo asks stream addons for: a Stremio type and id.
type titleRef struct {
	key       string // "movie:tt0111161", "series:tt0903747:1:2"
	typ       string // "movie" or "series"
	id        string // "tt0111161" or "tt0903747:1:2"
	contentID string // movie content id, or "" for an episode
	episodeID string // episode content id, or ""
	season    int
	episode   int
}

// movieRef resolves a movie content id to its Stremio id.
func (s *Service) movieRef(ctx context.Context, contentID string) (*titleRef, error) {
	var typ, imdb string
	err := s.store.db.QueryRow(ctx, `SELECT type, imdb_id FROM media_items WHERE content_id = $1`, contentID).Scan(&typ, &imdb)
	if errors.Is(err, pgx.ErrNoRows) || typ != "movie" || !strings.HasPrefix(imdb, "tt") {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return &titleRef{key: "movie:" + imdb, typ: "movie", id: imdb, contentID: contentID}, nil
}

// episodeRef resolves an episode content id to "tt...:season:episode".
func (s *Service) episodeRef(ctx context.Context, episodeID string) (*titleRef, error) {
	var imdb string
	var season, episode int
	err := s.store.db.QueryRow(ctx, `
		SELECT mi.imdb_id, e.season_number, e.episode_number
		FROM episodes e JOIN media_items mi ON mi.content_id = e.series_id
		WHERE e.content_id = $1`, episodeID).Scan(&imdb, &season, &episode)
	if errors.Is(err, pgx.ErrNoRows) || !strings.HasPrefix(imdb, "tt") || episode <= 0 {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	id := fmt.Sprintf("%s:%d:%d", imdb, season, episode)
	return &titleRef{key: "series:" + id, typ: "series", id: id, episodeID: episodeID, season: season, episode: episode}, nil
}

// streamAddons are the enabled server-wide addons that serve streams for typ
// and IMDb ids.
func (s *Service) streamAddons(ctx context.Context, typ string) ([]*Addon, error) {
	all, err := s.store.List(ctx, nil)
	if err != nil {
		return nil, err
	}
	var out []*Addon
	for _, a := range all {
		if a.Enabled && a.Manifest.servesStreams(typ, "tt") {
			out = append(out, a)
		}
	}
	return out, nil
}

// servesStreams reports whether the manifest has a stream resource for typ
// and ids starting with prefix.
func (m *Manifest) servesStreams(typ, prefix string) bool {
	for _, r := range m.Resources {
		if r.Name != "stream" {
			continue
		}
		types := r.Types
		if types == nil {
			types = m.Types
		}
		prefixes := r.IDPrefixes
		if prefixes == nil {
			prefixes = m.IDPrefixes
		}
		if !slices.Contains(types, typ) {
			continue
		}
		if len(prefixes) == 0 || slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(prefix, p) || strings.HasPrefix(p, prefix) }) {
			return true
		}
	}
	return false
}

// FetchStreams returns an addon's streams for a title.
func (c *Client) FetchStreams(ctx context.Context, base, typ, id string) ([]Stream, error) {
	var body struct {
		Streams []Stream `json:"streams"`
	}
	if err := c.getJSON(ctx, resourceURL(base, "stream", typ, id, nil), &body); err != nil {
		return nil, err
	}
	return body.Streams, nil
}

type rankedStream struct {
	addon    *Addon
	addonPos int
	index    int
	stream   Stream
	info     StreamInfo
}

// rankStreams orders streams best first: higher resolution, then the
// addon's position on the Addons page, then the addon's own order (addons
// like AIOStreams already sort by cache status and quality).
func rankStreams(rs []rankedStream) {
	slices.SortStableFunc(rs, func(a, b rankedStream) int {
		if d := resolutionRank(b.info.Resolution) - resolutionRank(a.info.Resolution); d != 0 {
			return d
		}
		if d := a.addonPos - b.addonPos; d != 0 {
			return d
		}
		return a.index - b.index
	})
}

// streamSet tracks in-flight and recent stream lookups per title.
type streamSet struct {
	mu       sync.Mutex
	fresh    map[string]time.Time
	inflight map[string]chan struct{}
}

// EnsureStreams makes a title's addon streams available as versions:
// fetched from every stream addon in parallel, ranked, saved as media files
// in the Streaming library, and the best few probed. It waits at most
// streamsDeadline; addons that answer later are skipped this time.
func (s *Service) EnsureStreams(ctx context.Context, ref *titleRef) {
	if ref == nil || s.streamingLibrary == 0 {
		return
	}
	s.streams.mu.Lock()
	if t, ok := s.streams.fresh[ref.key]; ok && time.Since(t) < streamSetFresh {
		s.streams.mu.Unlock()
		return
	}
	if wait, busy := s.streams.inflight[ref.key]; busy {
		s.streams.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
		}
		return
	}
	done := make(chan struct{})
	s.streams.inflight[ref.key] = done
	s.streams.mu.Unlock()
	defer func() {
		s.streams.mu.Lock()
		delete(s.streams.inflight, ref.key)
		s.streams.mu.Unlock()
		close(done)
	}()

	// After a restart, a recent lookup in the database still counts.
	var fetchedAt time.Time
	if err := s.store.db.QueryRow(ctx, `SELECT fetched_at FROM quillo_stream_sets WHERE title_key = $1`, ref.key).Scan(&fetchedAt); err == nil && time.Since(fetchedAt) < streamSetFresh {
		s.markFresh(ref.key, fetchedAt)
		return
	}

	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), streamsDeadline)
	defer cancel()
	ranked, err := s.fetchRanked(sctx, ref)
	if err != nil {
		slog.WarnContext(ctx, "quillo streams: lookup failed", "title", ref.key, "error", err)
		return
	}
	files, err := s.saveStreams(sctx, ref, ranked)
	if err != nil {
		slog.WarnContext(ctx, "quillo streams: saving failed", "title", ref.key, "error", err)
		return
	}
	s.markFresh(ref.key, time.Now())
	if len(files) == 0 {
		return
	}
	// Probe every stream now, in parallel, so playback can plan them and dead
	// links are hidden before anyone picks one. Probes still running at the
	// deadline finish in the background.
	n := len(files)
	probeCtx, cancelProbes := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	all := make(chan struct{})
	go func() {
		defer cancelProbes()
		defer close(all)
		var wg sync.WaitGroup
		sem := make(chan struct{}, probeParallel)
		for _, f := range files[:n] {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				s.probeStream(probeCtx, f)
			}()
		}
		wg.Wait()
	}()
	select {
	case <-all:
	case <-time.After(probeDeadline):
	case <-ctx.Done():
	}
}

func (s *Service) markFresh(key string, at time.Time) {
	s.streams.mu.Lock()
	defer s.streams.mu.Unlock()
	if len(s.streams.fresh) > 5000 {
		clear(s.streams.fresh)
	}
	s.streams.fresh[key] = at
}

func (s *Service) fetchRanked(ctx context.Context, ref *titleRef) ([]rankedStream, error) {
	addons, err := s.streamAddons(ctx, ref.typ)
	if err != nil || len(addons) == 0 {
		return nil, err
	}
	results := make([][]rankedStream, len(addons))
	var wg sync.WaitGroup
	for i, a := range addons {
		wg.Add(1)
		go func() {
			defer wg.Done()
			streams, err := s.client.FetchStreams(ctx, BaseURL(a.ManifestURL), ref.typ, ref.id)
			if err != nil {
				slog.InfoContext(ctx, "quillo streams: addon skipped", "addon", a.Name, "title", ref.key, "error", err)
				return
			}
			for j, st := range streams {
				if !strings.HasPrefix(st.URL, "https://") && !strings.HasPrefix(st.URL, "http://") {
					continue
				}
				results[i] = append(results[i], rankedStream{addon: a, addonPos: i, index: j, stream: st, info: ParseStream(st)})
			}
		}()
	}
	wg.Wait()
	var all []rankedStream
	for _, r := range results {
		all = append(all, r...)
	}
	rankStreams(all)
	if len(all) > maxStreamsPerTitle {
		all = all[:maxStreamsPerTitle]
	}
	return all, nil
}

// streamPath is the placeholder file path of a stream: stable for the same
// title, addon and URL, ending in the version label and the file's
// extension. The title is part of it because a file row belongs to one title,
// and an addon may serve the same URL for several (a season pack).
func streamPath(titleKey string, addonID int64, rawURL string, info StreamInfo) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", titleKey, addonID, rawURL)))
	return streamPathPrefix + hex.EncodeToString(sum[:8]) + "/" + safeLabel(info.Label()) + "." + extFor(info.Container())
}

func extFor(container string) string {
	switch container {
	case "mpegts":
		return "ts"
	}
	return container
}

// saveStreams writes the ranked streams as media files and retires the
// title's earlier streams that weren't returned this time. It returns the
// saved files in rank order.
func (s *Service) saveStreams(ctx context.Context, ref *titleRef, ranked []rankedStream) ([]*models.MediaFile, error) {
	files := make([]*models.MediaFile, 0, len(ranked))
	keep := make([]string, 0, len(ranked))
	for rank, r := range ranked {
		mf := models.MediaFile{
			ContentID:     ref.contentID,
			EpisodeID:     ref.episodeID,
			SeasonNumber:  ref.season,
			EpisodeNumber: ref.episode,
			MediaFolderID: s.streamingLibrary,
			FilePath:      streamPath(ref.key, r.addon.ID, r.stream.URL, r.info),
			FileSize:      r.info.Size,
			Resolution:    r.info.Resolution,
			HDR:           r.info.HDR != "",
			CodecVideo:    r.info.Codec,
			Container:     r.info.Container(),
		}
		saved, err := s.files.Upsert(ctx, mf)
		if err != nil {
			return nil, err
		}
		headers, _ := json.Marshal(r.stream.BehaviorHints.ProxyHeaders.Request)
		if string(headers) == "null" {
			headers = []byte("{}")
		}
		if _, err := s.store.db.Exec(ctx, `INSERT INTO quillo_streams (file_path, title_key, addon_id, url, headers, rank)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (file_path) DO UPDATE SET url = excluded.url, headers = excluded.headers,
				rank = excluded.rank, fetched_at = now()`,
			saved.FilePath, ref.key, r.addon.ID, r.stream.URL, headers, rank); err != nil {
			return nil, err
		}
		if _, err := s.store.db.Exec(ctx, `UPDATE media_files SET missing_since = NULL WHERE file_path = $1 AND missing_since IS NOT NULL`, saved.FilePath); err != nil {
			return nil, err
		}
		s.remote.forget(saved.FilePath)
		files = append(files, saved)
		keep = append(keep, saved.FilePath)
	}
	// Streams an addon no longer offers are hidden, not deleted: a viewer may
	// be playing one right now.
	if _, err := s.store.db.Exec(ctx, `UPDATE media_files SET missing_since = now()
		WHERE missing_since IS NULL AND file_path IN (
			SELECT file_path FROM quillo_streams WHERE title_key = $1 AND NOT (file_path = ANY($2)))`,
		ref.key, keep); err != nil {
		return nil, err
	}
	if _, err := s.store.db.Exec(ctx, `INSERT INTO quillo_stream_sets (title_key, fetched_at, stream_count)
		VALUES ($1, now(), $2) ON CONFLICT (title_key) DO UPDATE SET fetched_at = now(), stream_count = excluded.stream_count`,
		ref.key, len(files)); err != nil {
		return nil, err
	}
	return files, nil
}

// probeStream reads a stream's container, tracks and duration over HTTP and
// saves them, so playback can plan it like a scanned file.
func (s *Service) probeStream(ctx context.Context, mf *models.MediaFile) {
	src, ok, err := s.remote.resolve(ctx, mf.FilePath)
	if err != nil || !ok || len(src.Headers) > 0 {
		return // ffprobe can't send the headers an addon asked for
	}
	var done bool
	_ = s.store.db.QueryRow(ctx, `SELECT probed_at IS NOT NULL FROM quillo_streams WHERE file_path = $1`, mf.FilePath).Scan(&done)
	if done {
		return
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return
	}
	probe, err := scanner.ProbeFile(ctx, ffprobe, src.URL)
	if err != nil {
		_, _ = s.store.db.Exec(ctx, `UPDATE quillo_streams SET probe_error = $2 WHERE file_path = $1`, mf.FilePath, truncate(err.Error(), 500))
		if ctx.Err() == nil {
			// The link answered with an error or isn't media: hide it, so
			// playback never picks a stream that can't play. A later lookup
			// shows it again if the addon still offers it.
			_, _ = s.store.db.Exec(ctx, `UPDATE media_files SET missing_since = now() WHERE file_path = $1 AND missing_since IS NULL`, mf.FilePath)
		}
		slog.InfoContext(ctx, "quillo streams: probe failed; hiding the stream", "file", path.Base(mf.FilePath), "error", err)
		return
	}
	probed := *mf
	scanner.ApplyProbe(&probed, probe)
	if probed.FileSize == 0 {
		probed.FileSize = mf.FileSize
	}
	if _, err := s.files.Upsert(ctx, probed); err != nil {
		slog.WarnContext(ctx, "quillo streams: saving probe", "error", err)
		return
	}
	_, _ = s.store.db.Exec(ctx, `UPDATE quillo_streams SET probed_at = now(), probe_error = NULL WHERE file_path = $1`, mf.FilePath)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// remoteSources resolves placeholder paths to stream URLs for playback,
// with a small in-memory cache.
type remoteSources struct {
	s     *Service
	mu    sync.Mutex
	cache map[string]playback.RemoteSource
}

func (r *remoteSources) forget(filePath string) {
	r.mu.Lock()
	delete(r.cache, filePath)
	r.mu.Unlock()
}

func (r *remoteSources) resolve(ctx context.Context, filePath string) (playback.RemoteSource, bool, error) {
	if !strings.HasPrefix(filePath, streamPathPrefix) {
		return playback.RemoteSource{}, false, nil
	}
	r.mu.Lock()
	src, ok := r.cache[filePath]
	r.mu.Unlock()
	if ok {
		return src, true, nil
	}
	var url string
	var headers map[string]string
	err := r.s.store.db.QueryRow(ctx, `SELECT url, headers FROM quillo_streams WHERE file_path = $1`, filePath).Scan(&url, &headers)
	if errors.Is(err, pgx.ErrNoRows) {
		return playback.RemoteSource{}, false, errors.New("this stream is no longer available")
	}
	if err != nil {
		return playback.RemoteSource{}, false, err
	}
	src = playback.RemoteSource{URL: url, Headers: headers}
	r.mu.Lock()
	if len(r.cache) > 2000 {
		clear(r.cache)
	}
	r.cache[filePath] = src
	r.mu.Unlock()
	return src, true, nil
}
