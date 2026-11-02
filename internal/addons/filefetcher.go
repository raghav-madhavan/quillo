package addons

import (
	"context"
	"log/slog"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

// StreamFiles is Silo's file repository with addon streams added: before a
// movie's or episode's files are read, the title's addon streams are made
// available as versions. Every other repository method is the embedded one,
// so the optional capabilities Silo checks for stay intact.
type StreamFiles struct {
	*scanner.FileRepository
	svc *Service
}

// StreamFiles wraps repo so detail pages and playback see addon streams.
func (s *Service) StreamFiles(repo *scanner.FileRepository) *StreamFiles {
	return &StreamFiles{FileRepository: repo, svc: s}
}

func (f *StreamFiles) GetByContentID(ctx context.Context, contentID string) ([]*models.MediaFile, error) {
	if ref, err := f.svc.movieRef(ctx, contentID); err != nil {
		slog.WarnContext(ctx, "quillo streams: resolving movie", "content_id", contentID, "error", err)
	} else if ref != nil {
		f.svc.EnsureStreams(ctx, ref)
	}
	return f.FileRepository.GetByContentID(ctx, contentID)
}

func (f *StreamFiles) GetByEpisodeID(ctx context.Context, episodeID string) ([]*models.MediaFile, error) {
	if ref, err := f.svc.episodeRef(ctx, episodeID); err != nil {
		slog.WarnContext(ctx, "quillo streams: resolving episode", "episode_id", episodeID, "error", err)
	} else if ref != nil {
		f.svc.EnsureStreams(ctx, ref)
	}
	return f.FileRepository.GetByEpisodeID(ctx, episodeID)
}
