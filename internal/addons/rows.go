package addons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/sections/recipes"
)

// SectionType is the home-row type for an addon catalog. Its config names the
// addon and catalog: {"addon":"12","type":"movie","catalog":"top"}.
const SectionType sections.SectionType = "quillo_addon_catalog"

const rowItemLimit = 50

type rowConfig struct {
	Addon   string `json:"addon"`
	Type    string `json:"type"`
	Catalog string `json:"catalog"`
}

func parseRowConfig(raw json.RawMessage) (rowConfig, int64, error) {
	var c rowConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, 0, errors.New("quillo_addon_catalog: config must be an object")
	}
	id, err := strconv.ParseInt(c.Addon, 10, 64)
	if err != nil || id <= 0 || c.Type == "" || c.Catalog == "" {
		return c, 0, errors.New("quillo_addon_catalog: config needs addon, type and catalog")
	}
	return c, id, nil
}

// addonRecipe registers the row type with Silo's section editor, so admins
// can rename, move or hide addon rows like any other. It is hidden from the
// add-row gallery: addon rows are created from the Addons page.
type addonRecipe struct{}

func (addonRecipe) Type() string                   { return string(SectionType) }
func (addonRecipe) NewParams() any                 { return &rowConfig{} }
func (addonRecipe) DefaultCacheTTL() time.Duration { return catalogFresh }
func (addonRecipe) Resolve(recipes.ResolverContext) (recipes.ResolvedItems, error) {
	// FetchOne hands this type to the registered external fetch instead.
	return recipes.ResolvedItems{}, errors.New("quillo_addon_catalog resolves through sections.RegisterExternalSection")
}
func (addonRecipe) Validate(raw json.RawMessage) error {
	_, _, err := parseRowConfig(raw)
	return err
}
func (addonRecipe) Definition() recipes.RecipeDefinition {
	return recipes.RecipeDefinition{
		Type:      string(SectionType),
		Category:  recipes.CategoryDiscovery,
		AdminOnly: true,
		Hidden:    true,
		Presets: []recipes.GalleryPreset{{
			Key: "quillo_addon_catalog", DisplayName: "Addon catalog", Icon: "🧩",
			DescriptionShort: "Titles from an addon catalog.", DefaultParams: json.RawMessage(`{}`),
		}},
	}
}

func init() {
	recipes.Register(addonRecipe{})
}

// fetchRow serves an addon home row.
func (s *Service) fetchRow(ctx context.Context, resolved sections.ResolvedSection, _ int, _ string, filter catalog.AccessFilter) (sections.SectionWithItems, error) {
	empty := sections.SectionWithItems{ResolvedSection: resolved}
	cfg, addonID, err := parseRowConfig(resolved.Config)
	if err != nil {
		return empty, nil
	}
	a, err := s.store.Get(ctx, addonID, nil)
	if errors.Is(err, ErrNotFound) {
		return empty, nil
	}
	if err != nil || !a.Enabled {
		return empty, err
	}
	ids, err := s.catalogs.ContentIDs(ctx, a, cfg.Type, cfg.Catalog, nil)
	if err != nil {
		// A broken addon shows as an empty (hidden) row, never as an error
		// that fails the whole home screen.
		return empty, nil
	}
	limit := resolved.ItemLimit
	if limit <= 0 || limit > rowItemLimit {
		limit = rowItemLimit
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	items, err := s.items.GetByIDsWithAccess(ctx, ids, filter)
	if err != nil {
		return empty, err
	}
	// GetByIDsWithAccess sorts by id; put the addon's order back.
	byID := make(map[string]int, len(ids))
	for i, id := range ids {
		byID[id] = i
	}
	ordered := make([]*models.MediaItem, len(ids))
	for _, it := range items {
		ordered[byID[it.ContentID]] = it
	}
	out := ordered[:0]
	for _, it := range ordered {
		if it != nil {
			out = append(out, it)
		}
	}
	empty.Items = out
	empty.TotalCount = len(out)
	return empty, nil
}

var rowIDUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func rowIDPrefix(addonID int64) string { return fmt.Sprintf("quillo-%d-", addonID) }

func rowTitle(c Catalog) string {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = c.ID
	}
	switch c.Type {
	case "movie":
		return name + " Movies"
	case "series":
		return name + " Series"
	}
	return name
}

// CreateRows adds the addon's listable catalogs to the home screen, right
// after Continue Watching, once per addon.
func (s *Service) CreateRows(ctx context.Context, a *Addon) error {
	var cats []Catalog
	for _, c := range a.Manifest.Catalogs {
		if _, ok := itemType(c.Type); ok && !c.RequiresExtra() {
			cats = append(cats, c)
		}
	}
	return pgx.BeginFunc(ctx, s.store.db, func(tx pgx.Tx) error {
		var done bool
		if err := tx.QueryRow(ctx, `SELECT home_rows_created FROM quillo_addons WHERE id = $1 FOR UPDATE`, a.ID).Scan(&done); err != nil {
			return err
		}
		if done {
			return nil
		}
		if len(cats) > 0 {
			// Rows go after Continue Watching (or first when there is none),
			// following earlier addon rows.
			var at int
			if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(position) + 1, 0) FROM page_sections
				WHERE scope = 'home' AND (section_type = 'continue_watching' OR section_type = $1)`,
				string(SectionType)).Scan(&at); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE page_sections SET position = position + $1
				WHERE scope = 'home' AND position >= $2`, len(cats), at); err != nil {
				return err
			}
			for i, c := range cats {
				id := rowIDPrefix(a.ID) + rowIDUnsafe.ReplaceAllString(c.Type+"-"+c.ID, "_")
				cfg, _ := json.Marshal(rowConfig{Addon: strconv.FormatInt(a.ID, 10), Type: c.Type, Catalog: c.ID})
				itemTypes, _ := itemType(c.Type)
				if _, err := tx.Exec(ctx, `INSERT INTO page_sections
					(id, scope, position, section_type, title, item_limit, config, media_types)
					VALUES ($1, 'home', $2, $3, $4, $5, $6, $7)
					ON CONFLICT (id) DO NOTHING`,
					id, at+i, string(SectionType), rowTitle(c), rowItemLimit, cfg, []string{itemTypes}); err != nil {
					return err
				}
			}
		}
		_, err := tx.Exec(ctx, `UPDATE quillo_addons SET home_rows_created = true WHERE id = $1`, a.ID)
		return err
	})
}

// DeleteRows removes an addon's home rows.
func (s *Service) DeleteRows(ctx context.Context, addonID int64) error {
	_, err := s.store.db.Exec(ctx, `DELETE FROM page_sections WHERE section_type = $1 AND id LIKE $2`,
		string(SectionType), rowIDPrefix(addonID)+"%")
	return err
}
