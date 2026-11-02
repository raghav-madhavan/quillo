package addons

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Addon resource requests are bounded so one slow addon never holds up a
// screen; the caller shows whatever the other addons returned.
const (
	resourceTimeout  = 8 * time.Second
	maxResourceBytes = 8 << 20
)

// FlexString decodes a JSON string or number as a string. Addons disagree on
// whether ids like moviedb_id are numbers or strings.
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = FlexString(strings.TrimSpace(s))
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return nil // an unexpected shape is treated as absent, not as a broken response
	}
	*f = FlexString(n.String())
	return nil
}

// MetaItem is a Stremio meta object, as found in catalog responses (a
// preview) and meta responses (full, with videos for series).
type MetaItem struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Name        string     `json:"name"`
	IMDbID      FlexString `json:"imdb_id"`
	MovieDBID   FlexString `json:"moviedb_id"`
	TVDBID      FlexString `json:"tvdb_id"`
	Poster      string     `json:"poster"`
	Background  string     `json:"background"`
	Logo        string     `json:"logo"`
	Description string     `json:"description"`
	ReleaseInfo FlexString `json:"releaseInfo"`
	Year        FlexString `json:"year"`
	Runtime     FlexString `json:"runtime"`
	Genres      []string   `json:"genres"`
	IMDbRating  FlexString `json:"imdbRating"`
	Status      string     `json:"status"`
	Videos      []Video    `json:"videos"`
}

// Video is an episode of a series meta.
type Video struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Title     string     `json:"title"`
	Season    int        `json:"season"`
	Episode   int        `json:"episode"`
	Number    int        `json:"number"`
	Released  string     `json:"released"`
	Overview  string     `json:"overview"`
	Thumbnail string     `json:"thumbnail"`
	TVDBID    FlexString `json:"tvdb_id"`
}

// IMDb returns the title's IMDb id, from imdb_id or from a tt-prefixed id.
func (m *MetaItem) IMDb() string {
	if id := string(m.IMDbID); strings.HasPrefix(id, "tt") {
		return id
	}
	if strings.HasPrefix(m.ID, "tt") {
		return m.ID
	}
	return ""
}

// YearInt is the first year in year or releaseInfo ("2008–2013" → 2008).
func (m *MetaItem) YearInt() int {
	for _, s := range []string{string(m.Year), string(m.ReleaseInfo)} {
		if len(s) >= 4 {
			if y, err := strconv.Atoi(s[:4]); err == nil && y > 1800 {
				return y
			}
		}
	}
	return 0
}

// RuntimeMinutes parses runtime strings like "109 min" or "1h 49min".
func (m *MetaItem) RuntimeMinutes() int {
	s := strings.ToLower(string(m.Runtime))
	if s == "" {
		return 0
	}
	total, num := 0, 0
	seen := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			num = num*10 + int(c-'0')
			seen = true
		case c == 'h' && seen:
			total += num * 60
			num, seen = 0, false
		case c == 'm' && seen:
			total += num
			num, seen = 0, false
		}
	}
	if seen {
		total += num // a bare number is minutes
	}
	return total
}

// Rating returns imdbRating as a number, or nil.
func (m *MetaItem) Rating() *float64 {
	r, err := strconv.ParseFloat(string(m.IMDbRating), 64)
	if err != nil || r <= 0 || r > 10 {
		return nil
	}
	return &r
}

// resourceURL builds {base}/{resource}/{type}/{id}[/{extra}].json. Extra keys
// are sorted so equal requests share a cache entry and an addon-side cache.
func resourceURL(base, resource, typ, id string, extra map[string]string) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(base, "/"))
	b.WriteString("/" + resource + "/" + url.PathEscape(typ) + "/" + url.PathEscape(id))
	if len(extra) > 0 {
		keys := make([]string, 0, len(extra))
		for k := range extra {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, url.PathEscape(k)+"="+url.PathEscape(extra[k]))
		}
		b.WriteString("/" + strings.Join(parts, "&"))
	}
	b.WriteString(".json")
	return b.String()
}

// FetchCatalog returns one page of an addon catalog.
func (c *Client) FetchCatalog(ctx context.Context, base, typ, id string, extra map[string]string) ([]MetaItem, error) {
	var body struct {
		Metas []MetaItem `json:"metas"`
	}
	if err := c.getJSON(ctx, resourceURL(base, "catalog", typ, id, extra), &body); err != nil {
		return nil, err
	}
	return body.Metas, nil
}

// FetchMeta returns an addon's full meta for one title.
func (c *Client) FetchMeta(ctx context.Context, base, typ, id string) (*MetaItem, error) {
	var body struct {
		Meta *MetaItem `json:"meta"`
	}
	if err := c.getJSON(ctx, resourceURL(base, "meta", typ, id, nil), &body); err != nil {
		return nil, err
	}
	if body.Meta == nil {
		return nil, userErrorf("The addon has no details for that title.")
	}
	return body.Meta, nil
}

func (c *Client) getJSON(ctx context.Context, u string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, resourceTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return userErrorf("The addon's link can't be opened.")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return fetchError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return userErrorf("The addon answered with status %d.", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResourceBytes+1))
	if err != nil {
		return fetchError(err)
	}
	if len(b) > maxResourceBytes {
		return userErrorf("The addon sent a response that was too large.")
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decoding addon response: %w", userErrorf("The addon sent a response Quillo couldn't read."))
	}
	return nil
}
