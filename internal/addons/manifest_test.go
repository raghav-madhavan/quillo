package addons

import (
	"errors"
	"testing"
)

func TestNormalizeManifestURL(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "https://v3-cinemeta.strem.io/manifest.json", want: "https://v3-cinemeta.strem.io/manifest.json"},
		{in: "  https://v3-cinemeta.strem.io/manifest.json  ", want: "https://v3-cinemeta.strem.io/manifest.json"},
		{in: "stremio://v3-cinemeta.strem.io/manifest.json", want: "https://v3-cinemeta.strem.io/manifest.json"},
		{in: "https://aio.example.com/stremio/abc/eyJ0/manifest.json", want: "https://aio.example.com/stremio/abc/eyJ0/manifest.json"},
		{in: "https://aio.example.com/stremio/abc/eyJ0", want: "https://aio.example.com/stremio/abc/eyJ0/manifest.json"},
		{in: "https://aio.example.com/stremio/abc/eyJ0/", want: "https://aio.example.com/stremio/abc/eyJ0/manifest.json"},
		{in: "https://addon.example.com/configure", want: "https://addon.example.com/manifest.json"},
		{in: "http://192.168.1.5:3000/manifest.json", want: "http://192.168.1.5:3000/manifest.json"},
		{in: "https://addon.example.com/manifest.json?x=1", want: "https://addon.example.com/manifest.json?x=1"},
		{in: "https://aio.example.com/stremio/a%2Fb%3D%3D", want: "https://aio.example.com/stremio/a%2Fb%3D%3D/manifest.json"},
		{in: "https://torrentio.example.com/sort=qualitysize|qualityfilter=480p/configure", want: "https://torrentio.example.com/sort=qualitysize%7Cqualityfilter=480p/manifest.json"},
		{in: "", wantErr: true},
		{in: "ftp://addon.example.com/manifest.json", wantErr: true},
		{in: "not a url", wantErr: true},
		{in: "https://user:pass@addon.example.com/manifest.json", wantErr: true},
	}
	for _, tt := range tests {
		got, err := NormalizeManifestURL(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("NormalizeManifestURL(%q) = %q, want error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("NormalizeManifestURL(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestBaseURL(t *testing.T) {
	if got := BaseURL("https://a.example.com/x/manifest.json"); got != "https://a.example.com/x" {
		t.Fatalf("BaseURL = %q", got)
	}
	if got := BaseURL("https://a.example.com/manifest.json?k=v"); got != "https://a.example.com" {
		t.Fatalf("BaseURL with query = %q", got)
	}
}

func TestParseManifest(t *testing.T) {
	cinemeta := `{
		"id": "com.linvo.cinemeta", "version": "3.0.13", "name": "Cinemeta",
		"resources": ["catalog", {"name": "meta", "types": ["movie", "series"], "idPrefixes": ["tt"]}, "addon_catalog"],
		"types": ["movie", "series"],
		"catalogs": [
			{"type": "movie", "id": "top", "name": "Popular", "extra": [{"name": "search"}, {"name": "genre", "options": ["Action"]}, {"name": "skip"}]},
			{"type": "series", "id": "top", "name": "Popular", "extraSupported": ["search", "skip"]},
			{"type": "movie", "id": "year", "extra": [{"name": "genre", "isRequired": true}]}
		]
	}`
	m, err := ParseManifest([]byte(cinemeta))
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasResource("catalog") || !m.HasResource("meta") || m.HasResource("stream") {
		t.Fatalf("resources = %+v", m.Resources)
	}
	if len(m.Catalogs) != 3 {
		t.Fatalf("catalogs = %d", len(m.Catalogs))
	}
	if !m.Catalogs[0].SupportsExtra("search") || !m.Catalogs[1].SupportsExtra("search") {
		t.Fatal("search support lost for extra or extraSupported")
	}
	if m.Catalogs[0].RequiresExtra() || !m.Catalogs[2].RequiresExtra() {
		t.Fatal("required extra misread")
	}
	if s := m.Summary(); s.Catalogs != 3 || s.HomeRowCatalogs != 2 || s.Streams || !s.Metadata || !s.Search {
		t.Fatalf("summary = %+v", s)
	}
}

func TestParseManifestRejects(t *testing.T) {
	tests := map[string]string{
		"not json":         `<html>`,
		"missing id":       `{"name":"X","version":"1","resources":["stream"],"types":["movie"]}`,
		"missing name":     `{"id":"x","version":"1","resources":["stream"],"types":["movie"]}`,
		"no resources":     `{"id":"x","name":"X","version":"1","resources":[],"types":["movie"]}`,
		"bad resource":     `{"id":"x","name":"X","version":"1","resources":[42],"types":["movie"]}`,
		"needs configure":  `{"id":"x","name":"X","version":"1","resources":["stream"],"types":["movie"],"behaviorHints":{"configurationRequired":true}}`,
		"nothing playable": `{"id":"x","name":"X","version":"1","resources":["subtitles"],"types":["movie"]}`,
	}
	for name, body := range tests {
		if _, err := ParseManifest([]byte(body)); err == nil {
			t.Errorf("%s: want error", name)
		} else if !errors.As(err, new(*UserError)) {
			t.Errorf("%s: error %v is not a UserError", name, err)
		}
	}
}
