package goyt

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseHLS(t *testing.T) {
	base, _ := url.Parse("https://media.test/path/index.m3u8?token=abc")

	playlist, err := ParseHLS([]byte(`#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:4
#EXTINF:4,
first.ts
#EXTINF:2.5,
../second.ts?part=2
#EXT-X-ENDLIST
`), base)
	if err != nil {
		t.Fatal(err)
	}

	if len(playlist.Segments) != 2 ||
		playlist.Duration != 6500*time.Millisecond {
		t.Fatalf("incorrect playlist: %+v", playlist)
	}

	if playlist.Segments[0].URL != "https://media.test/path/first.ts" ||
		playlist.Segments[1].URL != "https://media.test/second.ts?part=2" {
		t.Fatal("incorrect relative URL resolution")
	}
}

func TestRejectUnsupportedHLS(t *testing.T) {
	base, _ := url.Parse("https://media.test/index.m3u8")

	for _, tag := range []string{
		`#EXT-X-KEY:METHOD=AES-128,URI="key"`,
		`#EXT-X-MAP:URI="init.mp4"`,
		"#EXT-X-BYTERANGE:188@0",
		"#EXT-X-DISCONTINUITY",
		"#EXT-X-STREAM-INF:BANDWIDTH=100000",
	} {
		t.Run(tag, func(t *testing.T) {
			input := "#EXTM3U\n#EXT-X-TARGETDURATION:4\n" +
				tag + "\n#EXTINF:4,\nsegment.ts\n#EXT-X-ENDLIST\n"

			if _, err := ParseHLS([]byte(input), base); err == nil {
				t.Fatal("unsupported feature was accepted")
			}
		})
	}
}

func TestRejectUnfinishedPlaylist(t *testing.T) {
	base, _ := url.Parse("https://media.test/index.m3u8")

	_, err := ParseHLS([]byte(`#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
segment.ts
`), base)

	if err == nil {
		t.Fatal("playlist without ENDLIST was accepted")
	}
}

func TestAppendTSSegment(t *testing.T) {
	packet := make([]byte, 188)
	packet[0] = 0x47
	data := append(append([]byte{}, packet...), packet...)

	path := filepath.Join(t.TempDir(), "segment.ts")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := appendTSSegment(context.Background(), &output, path); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(output.Bytes(), data) {
		t.Fatal("segment bytes changed")
	}

	if err := os.WriteFile(path, []byte(strings.Repeat("x", 188)), 0600); err != nil {
		t.Fatal(err)
	}

	if err := appendTSSegment(context.Background(), &output, path); err == nil {
		t.Fatal("invalid transport stream accepted")
	}
}
