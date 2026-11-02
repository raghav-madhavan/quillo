package addons

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// UserError is a failure the admin can act on. Its message is plain English
// and safe to show in the web UI as it is.
type UserError struct{ Message string }

func (e *UserError) Error() string { return e.Message }

func userErrorf(format string, args ...any) *UserError {
	return &UserError{Message: fmt.Sprintf(format, args...)}
}

// NormalizeManifestURL turns what an admin pasted into the manifest URL Quillo
// stores: https for stremio:// links, ending in /manifest.json. A configure
// page URL (".../configure") becomes that addon's manifest.
func NormalizeManifestURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", userErrorf("Paste the addon's manifest link.")
	}
	if rest, ok := strings.CutPrefix(raw, "stremio://"); ok {
		raw = "https://" + rest
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", userErrorf("That doesn't look like a link. Addon links start with https:// and usually end in /manifest.json.")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", userErrorf("Addon links must start with https:// or http://.")
	}
	if u.User != nil {
		return "", userErrorf("Addon links can't contain a username or password.")
	}
	u.Fragment = ""
	// Work on the escaped path so an addon's encoded config segment comes
	// back byte for byte.
	escaped := strings.TrimRight(u.EscapedPath(), "/")
	escaped = strings.TrimSuffix(escaped, "/configure")
	if !strings.HasSuffix(escaped, "/manifest.json") {
		escaped += "/manifest.json"
	}
	path, err := url.PathUnescape(escaped)
	if err != nil {
		return "", userErrorf("That link has a broken character in it. Copy it again from the addon.")
	}
	u.Path, u.RawPath = path, escaped
	return u.String(), nil
}

// BaseURL is the addon's resource root: the manifest URL without
// /manifest.json or its query. Resources live at {base}/{resource}/....
func BaseURL(manifestURL string) string {
	u, err := url.Parse(manifestURL)
	if err != nil {
		return strings.TrimSuffix(manifestURL, "/manifest.json")
	}
	u.RawQuery = ""
	u.Fragment = ""
	s := u.String()
	return strings.TrimSuffix(s, "/manifest.json")
}

// Manifest is the part of a Stremio addon manifest Quillo uses. The raw JSON
// is stored as served, so fields not modeled here are kept.
type Manifest struct {
	ID            string        `json:"id"`
	Version       string        `json:"version"`
	Name          string        `json:"name"`
	Description   string        `json:"description,omitempty"`
	Logo          string        `json:"logo,omitempty"`
	Resources     []Resource    `json:"resources"`
	Types         []string      `json:"types"`
	Catalogs      []Catalog     `json:"catalogs"`
	IDPrefixes    []string      `json:"idPrefixes,omitempty"`
	BehaviorHints BehaviorHints `json:"behaviorHints"`
}

type BehaviorHints struct {
	Adult                 bool `json:"adult,omitempty"`
	P2P                   bool `json:"p2p,omitempty"`
	Configurable          bool `json:"configurable,omitempty"`
	ConfigurationRequired bool `json:"configurationRequired,omitempty"`
}

// Resource is one entry of manifest.resources, which addons write either as a
// bare name ("stream") or as an object narrowing the types and id prefixes.
type Resource struct {
	Name       string   `json:"name"`
	Types      []string `json:"types,omitempty"`
	IDPrefixes []string `json:"idPrefixes,omitempty"`
}

func (r *Resource) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err == nil {
		r.Name = name
		return nil
	}
	type plain Resource
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("resource must be a name or an object: %w", err)
	}
	*r = Resource(p)
	return nil
}

type Catalog struct {
	Type  string         `json:"type"`
	ID    string         `json:"id"`
	Name  string         `json:"name,omitempty"`
	Extra []CatalogExtra `json:"extra,omitempty"`
	// Older manifests list extras by name instead of Extra.
	ExtraSupported []string `json:"extraSupported,omitempty"`
	ExtraRequired  []string `json:"extraRequired,omitempty"`
}

type CatalogExtra struct {
	Name       string   `json:"name"`
	IsRequired bool     `json:"isRequired,omitempty"`
	Options    []string `json:"options,omitempty"`
}

// SupportsExtra reports whether the catalog accepts the named extra
// ("search", "skip", "genre").
func (c Catalog) SupportsExtra(name string) bool {
	if slices.Contains(c.ExtraSupported, name) {
		return true
	}
	return slices.ContainsFunc(c.Extra, func(e CatalogExtra) bool { return e.Name == name })
}

// RequiresExtra reports whether the catalog can't be listed without an extra,
// such as a search-only catalog. Those can't be home rows.
func (c Catalog) RequiresExtra() bool {
	if len(c.ExtraRequired) > 0 {
		return true
	}
	return slices.ContainsFunc(c.Extra, func(e CatalogExtra) bool { return e.IsRequired })
}

func (m *Manifest) HasResource(name string) bool {
	return slices.ContainsFunc(m.Resources, func(r Resource) bool { return r.Name == name })
}

// ManifestSummary is what the Addons page shows about an addon.
type ManifestSummary struct {
	Catalogs        int      `json:"catalogs"`
	HomeRowCatalogs int      `json:"home_row_catalogs"`
	Search          bool     `json:"search"`
	Metadata        bool     `json:"metadata"`
	Streams         bool     `json:"streams"`
	Types           []string `json:"types"`
	Configurable    bool     `json:"configurable"`
}

func (m *Manifest) Summary() ManifestSummary {
	s := ManifestSummary{
		Catalogs:     len(m.Catalogs),
		Metadata:     m.HasResource("meta"),
		Streams:      m.HasResource("stream"),
		Types:        m.Types,
		Configurable: m.BehaviorHints.Configurable,
	}
	if s.Types == nil {
		s.Types = []string{}
	}
	for _, c := range m.Catalogs {
		if !c.RequiresExtra() {
			s.HomeRowCatalogs++
		}
		if c.SupportsExtra("search") {
			s.Search = true
		}
	}
	return s
}

// ParseManifest decodes and checks a manifest. Every error is a *UserError.
func ParseManifest(body []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, userErrorf("That link didn't return an addon manifest. Check that it's the manifest link the addon gave you.")
	}
	switch {
	case strings.TrimSpace(m.ID) == "":
		return nil, userErrorf("This addon's manifest has no id, so it can't be installed.")
	case strings.TrimSpace(m.Name) == "":
		return nil, userErrorf("This addon's manifest has no name, so it can't be installed.")
	case len(m.Resources) == 0:
		return nil, userErrorf("This addon doesn't offer anything Quillo can use.")
	case m.BehaviorHints.ConfigurationRequired:
		return nil, userErrorf("This addon needs to be set up first. Open its configure page, finish setup, then paste the manifest link it gives you.")
	}
	if !m.HasResource("catalog") && !m.HasResource("meta") && !m.HasResource("stream") {
		return nil, userErrorf("This addon only offers %s, which Quillo doesn't use yet.", resourceNames(m.Resources))
	}
	return &m, nil
}

func resourceNames(rs []Resource) string {
	names := make([]string, 0, len(rs))
	for _, r := range rs {
		names = append(names, r.Name)
	}
	return strings.Join(names, ", ")
}
