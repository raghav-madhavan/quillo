package addons

import (
	"encoding/json"
	"testing"
)

func TestMetaItemParsing(t *testing.T) {
	var m MetaItem
	body := `{"id":"tt0903747","type":"series","name":"Breaking Bad","moviedb_id":1396,"tvdb_id":"81189",
		"imdb_id":null,"releaseInfo":"2008–2013","runtime":"1h 5min","imdbRating":"9.5","genres":["Drama"]}`
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if m.MovieDBID != "1396" || m.TVDBID != "81189" || m.IMDb() != "tt0903747" {
		t.Fatalf("ids = %q %q %q", m.MovieDBID, m.TVDBID, m.IMDb())
	}
	if m.YearInt() != 2008 || m.RuntimeMinutes() != 65 {
		t.Fatalf("year %d runtime %d", m.YearInt(), m.RuntimeMinutes())
	}
	if r := m.Rating(); r == nil || *r != 9.5 {
		t.Fatalf("rating = %v", r)
	}
}

func TestRuntimeMinutes(t *testing.T) {
	for in, want := range map[string]int{"109 min": 109, "2h": 120, "1h 49min": 109, "45": 45, "": 0, "N/A": 0} {
		m := MetaItem{Runtime: FlexString(in)}
		if got := m.RuntimeMinutes(); got != want {
			t.Errorf("RuntimeMinutes(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestResourceURL(t *testing.T) {
	if got := resourceURL("https://a.example.com/x/", "catalog", "movie", "top", nil); got != "https://a.example.com/x/catalog/movie/top.json" {
		t.Fatalf("got %q", got)
	}
	got := resourceURL("https://a.example.com", "catalog", "movie", "top", map[string]string{"skip": "50", "search": "star wars"})
	if got != "https://a.example.com/catalog/movie/top/search=star%20wars&skip=50.json" {
		t.Fatalf("got %q", got)
	}
	if got := resourceURL("https://a.example.com", "stream", "series", "tt1:1:2", nil); got != "https://a.example.com/stream/series/tt1:1:2.json" {
		t.Fatalf("got %q", got)
	}
}
