package addons

import "testing"

func TestParseStream(t *testing.T) {
	tests := []struct {
		name  string
		st    Stream
		label string
		cont  string
		codec string
		cache int
	}{
		{
			name: "torrentio style",
			st: Stream{Name: "Torrentio\n4k DV | HDR", Title: "Dune.Part.Two.2024.2160p.UHD.BluRay.x265-GROUP.mkv\n👤 42 💾 61.2 GB ⚙️ TPB",
				URL: "https://debrid.example.com/dl/abc/Dune.Part.Two.2024.2160p.mkv"},
			label: "2160p DV · 61.2 GB · Torrentio", cont: "mkv", codec: "hevc",
		},
		{
			name: "cached marker", st: Stream{Name: "[RD+] Torrentio\n1080p", Title: "x.mkv", URL: "https://a.example.com/x"},
			label: "1080p · [RD+] Torrentio", cont: "mkv", cache: 1,
		},
		{
			name: "uncached marker", st: Stream{Name: "[RD download] Torrentio\n720p", Title: "x.mkv", URL: "https://a.example.com/x"},
			label: "720p · [RD download] Torrentio", cont: "mkv", cache: -1,
		},
		{
			name: "comet uncached", st: Stream{Name: "[RD⬇️] Comet\n1080p", Title: "x.mkv", URL: "https://a.example.com/x"},
			label: "1080p · [RD⬇️] Comet", cont: "mkv", cache: -1,
		},
		{
			name: "mediafusion unavailable", st: Stream{Name: "[❌] realdebrid", Title: "x.mkv", URL: "https://a.example.com/x"},
			label: "[❌] realdebrid", cont: "mkv", cache: -1,
		},
		{
			name: "comet cached", st: Stream{Name: "[RD⚡] Comet\n2160p", Title: "x.mkv", URL: "https://a.example.com/x"},
			label: "2160p · [RD⚡] Comet", cont: "mkv", cache: 1,
		},
		{
			name: "size from behavior hints",
			st: Stream{Name: "AIOStreams", Title: "1080p WEB-DL H.264", URL: "https://cdn.example.com/v?id=1",
				BehaviorHints: StreamBehaviorHints{Filename: "Show.S01E02.1080p.WEB.mp4", VideoSize: "2147483648"}},
			label: "1080p · 2.0 GB · AIOStreams", cont: "mp4", codec: "h264",
		},
		{
			name: "nothing known", st: Stream{URL: "https://x.example.com/stream"},
			label: "Stream", cont: "mkv",
		},
	}
	for _, tt := range tests {
		info := ParseStream(tt.st)
		if got := info.Label(); got != tt.label {
			t.Errorf("%s: label %q, want %q", tt.name, got, tt.label)
		}
		if got := info.Container(); got != tt.cont {
			t.Errorf("%s: container %q, want %q", tt.name, got, tt.cont)
		}
		if info.Cache != tt.cache {
			t.Errorf("%s: cache %d, want %d", tt.name, info.Cache, tt.cache)
		}
		if info.Codec != tt.codec {
			t.Errorf("%s: codec %q, want %q", tt.name, info.Codec, tt.codec)
		}
	}
}

func TestRankStreams(t *testing.T) {
	in := []rankedStream{
		{addonPos: 1, index: 0, info: StreamInfo{Resolution: "2160p"}},
		{addonPos: 0, index: 1, info: StreamInfo{Resolution: "720p"}},
		{addonPos: 0, index: 0, info: StreamInfo{Resolution: "1080p"}},
		{addonPos: 0, index: 2, info: StreamInfo{}},
	}
	in = append(in, rankedStream{addonPos: 2, index: 0, info: StreamInfo{Resolution: "480p", Cache: 1}},
		rankedStream{addonPos: 0, index: 3, info: StreamInfo{Resolution: "2160p", Cache: -1}})
	rankStreams(in)
	want := []string{"480p", "2160p", "1080p", "720p", "", "2160p"}
	for i, r := range in {
		if r.info.Resolution != want[i] {
			t.Fatalf("order %d = %q, want %q (%+v)", i, r.info.Resolution, want[i], in)
		}
	}
}
