//go:build quillotestaddon

// A local Stremio stream addon for testing Quillo (dev only; see
// make-media.sh). It serves generated test clips for a few IMDb ids, with
// HTTP range support, plus one dead link to test that dead streams are hidden.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const base = "http://localhost:9100"

var streams = map[string][]map[string]any{
	// Hocus Pocus (addon-only movie)
	"movie/tt0107120": {
		{"name": "TestAddon\n1080p", "title": "Hocus.Pocus.1993.1080p.BluRay.x265.mkv\n💾 24.8 MB", "url": base + "/media/test-1080p-hevc.mkv",
			"behaviorHints": map[string]any{"filename": "Hocus.Pocus.1993.1080p.BluRay.x265.mkv"}},
		{"name": "TestAddon\n720p", "title": "Hocus.Pocus.1993.720p.WEB.x264.mp4", "url": base + "/media/test-720p-h264.mp4"},
	},
	// Big Buck Bunny (also in the local library)
	"movie/tt1254207": {
		{"name": "TestAddon\n1080p", "title": "Big.Buck.Bunny.2008.1080p.x265.mkv", "url": base + "/media/test-1080p-hevc.mkv"},
	},
	// Dexter: Resurrection S01E01
	"series/tt33043892:1:1": {
		{"name": "TestAddon\n1080p", "title": "Dexter.Resurrection.S01E01.1080p.WEB.x265.mkv", "url": base + "/media/test-1080p-hevc.mkv"},
		{"name": "TestAddon\n720p", "title": "Dexter.Resurrection.S01E01.720p.WEB.h264.mp4", "url": base + "/media/test-720p-h264.mp4"},
	},
	// Dexter: Resurrection S01E02, with DTS audio the phone can't decode, so
	// playback goes through a server remux.
	"series/tt33043892:1:2": {
		{"name": "[RD+] TestAddon\n1080p", "title": "Dexter.Resurrection.S01E02.1080p.WEB.h264.DTS.mkv", "url": base + "/media/test-1080p-h264-dts.mkv"},
	},
	// A dead link, to test falling back to the next stream.
	"movie/tt0111161": {
		{"name": "TestAddon\n2160p", "title": "Dead.Link.2160p.mkv", "url": base + "/media/missing.mkv"},
		{"name": "TestAddon\n720p", "title": "Working.720p.mp4", "url": base + "/media/test-720p-h264.mp4"},
	},
}

func main() {
	dir := filepath.Join(filepath.Dir(os.Args[0]), "media")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	http.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"id": "test.quillo.streams", "version": "1.0.0", "name": "Test Streams",
			"description": "Local test clips for Quillo development.",
			"resources":   []any{"stream"}, "types": []string{"movie", "series"},
			"idPrefixes": []string{"tt"}, "catalogs": []any{},
		})
	})
	http.HandleFunc("/stream/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/stream/"), ".json")
		log.Printf("stream request %s", key)
		list := streams[key]
		if list == nil {
			list = []map[string]any{}
		}
		writeJSON(w, map[string]any{"streams": list})
	})
	http.Handle("/media/", http.StripPrefix("/media/", http.FileServer(http.Dir(dir))))
	log.Printf("test addon on %s/manifest.json, media from %s", base, dir)
	log.Fatal(http.ListenAndServe("127.0.0.1:9100", nil))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(v)
}
