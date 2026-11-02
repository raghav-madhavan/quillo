package addons

import (
	"context"
	"log/slog"
)

// Preset is an addon the Addons page offers with one click.
type Preset struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ManifestURL string `json:"manifest_url"`
}

// Presets are catalog and metadata addons only. Quillo ships no stream
// sources: those are always the operator's own choice.
var Presets = []Preset{
	{
		Key:         "cinemeta",
		Name:        "Cinemeta",
		Description: "Popular, new and featured movies and series, with posters and episode lists. Made by the Stremio team.",
		ManifestURL: "https://v3-cinemeta.strem.io/manifest.json",
	},
}

const starterMetaKey = "starter_addons_installed"

// EnsureStarter installs the presets on a fresh server so the home screen has
// rows with no setup. It runs once: after it succeeds, or when an admin has
// already installed addons, it never runs again, so removing Cinemeta sticks.
// A network failure leaves it to try again at the next start.
func (s *Service) EnsureStarter(ctx context.Context) {
	if _, done, err := s.store.Meta(ctx, starterMetaKey); err != nil || done {
		return
	}
	existing, err := s.store.List(ctx, nil)
	if err != nil {
		slog.WarnContext(ctx, "quillo starter addons: listing", "error", err)
		return
	}
	if len(existing) == 0 {
		for _, p := range Presets {
			if _, err := s.Install(ctx, nil, p.ManifestURL); err != nil {
				slog.WarnContext(ctx, "quillo starter addons: will retry at next start", "addon", p.Name, "error", err)
				return
			}
			slog.InfoContext(ctx, "quillo starter addon installed", "addon", p.Name)
		}
	}
	if err := s.store.SetMeta(ctx, starterMetaKey, "true"); err != nil {
		slog.WarnContext(ctx, "quillo starter addons: saving marker", "error", err)
	}
}
