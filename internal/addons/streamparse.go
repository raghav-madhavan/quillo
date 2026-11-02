package addons

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Stream is one entry of an addon's stream response. Only streams with an
// http(s) url are usable: torrents (infoHash), YouTube ids and external
// links need a client Quillo doesn't have.
type Stream struct {
	Name          string              `json:"name"`
	Title         string              `json:"title"`
	Description   string              `json:"description"`
	URL           string              `json:"url"`
	InfoHash      string              `json:"infoHash"`
	BehaviorHints StreamBehaviorHints `json:"behaviorHints"`
}

type StreamBehaviorHints struct {
	Filename     string     `json:"filename"`
	VideoSize    FlexString `json:"videoSize"`
	NotWebReady  bool       `json:"notWebReady"`
	ProxyHeaders struct {
		Request map[string]string `json:"request"`
	} `json:"proxyHeaders"`
}

// StreamInfo is what Quillo reads out of a stream's free-text labels.
type StreamInfo struct {
	Resolution string // "2160p", "1080p", ...
	HDR        string // "DV", "HDR10+", "HDR"
	Codec      string // "hevc", "h264", "av1"
	Size       int64  // bytes, 0 when unknown
	Filename   string
	Source     string // the addon's own name line, e.g. "Torrentio"
}

var (
	reRes    = regexp.MustCompile(`(?i)\b(2160p|4k|uhd|1440p|1080p|720p|576p|480p)\b`)
	reDV     = regexp.MustCompile(`(?i)\b(dv|dovi|dolby[ ._-]?vision)\b`)
	reHDR10  = regexp.MustCompile(`(?i)\bhdr10\+|\bhdr10plus\b`)
	reHDR    = regexp.MustCompile(`(?i)\bhdr(10)?\b`)
	reHEVC   = regexp.MustCompile(`(?i)\b(hevc|x265|h[ .]?265)\b`)
	reAV1    = regexp.MustCompile(`(?i)\bav1\b`)
	reAVC    = regexp.MustCompile(`(?i)\b(avc|x264|h[ .]?264)\b`)
	reSize   = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)\s*(TB|GB|GiB|MB|MiB)\b`)
	videoExt = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".mov": true, ".avi": true, ".ts": true, ".m2ts": true, ".webm": true}
)

func normalizeResolution(s string) string {
	switch strings.ToLower(s) {
	case "4k", "uhd", "2160p":
		return "2160p"
	}
	return strings.ToLower(s)
}

func resolutionRank(r string) int {
	switch r {
	case "2160p":
		return 5
	case "1440p":
		return 4
	case "1080p":
		return 3
	case "720p":
		return 2
	case "576p", "480p":
		return 1
	}
	return 0
}

// ParseStream reads quality, size and filename from a stream's labels.
func ParseStream(st Stream) StreamInfo {
	text := strings.Join([]string{st.Name, st.Title, st.Description, st.BehaviorHints.Filename}, "\n")
	var info StreamInfo
	if m := reRes.FindString(text); m != "" {
		info.Resolution = normalizeResolution(m)
	}
	switch {
	case reDV.MatchString(text):
		info.HDR = "DV"
	case reHDR10.MatchString(text):
		info.HDR = "HDR10+"
	case reHDR.MatchString(text):
		info.HDR = "HDR"
	}
	switch {
	case reAV1.MatchString(text):
		info.Codec = "av1"
	case reHEVC.MatchString(text):
		info.Codec = "hevc"
	case reAVC.MatchString(text):
		info.Codec = "h264"
	}
	if n, err := strconv.ParseInt(string(st.BehaviorHints.VideoSize), 10, 64); err == nil && n > 0 {
		info.Size = n
	} else if m := reSize.FindStringSubmatch(text); m != nil {
		v, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		switch strings.ToUpper(m[2]) {
		case "TB":
			v *= 1 << 40
		case "GB", "GIB":
			v *= 1 << 30
		case "MB", "MIB":
			v *= 1 << 20
		}
		info.Size = int64(v)
	}
	info.Filename = streamFilename(st)
	if first, _, _ := strings.Cut(strings.TrimSpace(st.Name), "\n"); first != "" {
		info.Source = strings.TrimSpace(first)
	}
	return info
}

// streamFilename is the stream's file name when the addon gives one or the
// URL ends in one; it decides the container Quillo reports.
func streamFilename(st Stream) string {
	if f := strings.TrimSpace(st.BehaviorHints.Filename); f != "" {
		return path.Base(f)
	}
	if u, err := url.Parse(st.URL); err == nil {
		if base := path.Base(u.Path); videoExt[strings.ToLower(path.Ext(base))] {
			if unescaped, err := url.PathUnescape(base); err == nil {
				return unescaped
			}
			return base
		}
	}
	return ""
}

// Label is the version name the apps show: "2160p DV · 42.1 GB · Torrentio".
func (i StreamInfo) Label() string {
	var parts []string
	q := i.Resolution
	if i.HDR != "" {
		q = strings.TrimSpace(q + " " + i.HDR)
	}
	if q != "" {
		parts = append(parts, q)
	}
	if i.Size > 0 {
		parts = append(parts, humanSize(i.Size))
	}
	if i.Source != "" {
		parts = append(parts, i.Source)
	}
	if len(parts) == 0 {
		return "Stream"
	}
	return strings.Join(parts, " · ")
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<40:
		return fmt.Sprintf("%.1f TB", float64(n)/(1<<40))
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	default:
		return fmt.Sprintf("%d MB", n/(1<<20))
	}
}

// Container is the stream's container from its file name, defaulting to
// Matroska, which most release files are.
func (i StreamInfo) Container() string {
	switch strings.ToLower(path.Ext(i.Filename)) {
	case ".mp4", ".m4v":
		return "mp4"
	case ".mov":
		return "mov"
	case ".webm":
		return "webm"
	case ".avi":
		return "avi"
	case ".ts", ".m2ts":
		return "mpegts"
	}
	return "mkv"
}

// safeLabel makes a label usable as the last part of a file path.
func safeLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', 0:
			return '-'
		}
		return r
	}, s)
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
