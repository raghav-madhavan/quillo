package procgroup

import (
	"context"
	"sync/atomic"
)

// Quillo: media files that are not on disk (addon streams) have placeholder
// paths. A registered rewriter swaps such a path, when it is a whole
// argument, for a URL the tool can read, so every ffmpeg and ffprobe run
// works on them without each caller knowing (FORK_NOTES.md).

// ArgRewriter returns the replacement for one argument, or ok=false to keep it.
type ArgRewriter func(ctx context.Context, arg string) (replacement string, ok bool)

var argRewriter atomic.Pointer[ArgRewriter]

// RegisterArgRewriter installs the rewriter used by CommandContext.
func RegisterArgRewriter(r ArgRewriter) {
	argRewriter.Store(&r)
}

func rewriteArgs(ctx context.Context, args []string) []string {
	p := argRewriter.Load()
	if p == nil {
		return args
	}
	var out []string
	for i, a := range args {
		if replacement, ok := (*p)(ctx, a); ok {
			if out == nil {
				out = append([]string(nil), args...)
			}
			out[i] = replacement
		}
	}
	if out == nil {
		return args
	}
	return out
}
