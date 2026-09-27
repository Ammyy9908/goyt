package goyt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func packedAACFixture() []byte {
	owner := []byte(
		"com.apple.streaming.transportStreamTimestamp\x00",
	)

	// ID3v2.3 PRIV frame containing the owner and an eight-byte timestamp.
	payload := append(owner, make([]byte, 8)...)
	frame := []byte{
		'P', 'R', 'I', 'V',
		0, 0, 0, byte(len(payload)),
		0, 0,
	}
	frame = append(frame, payload...)

	header := []byte{
		'I', 'D', '3', 3, 0, 0,
		0, 0, 0, byte(len(frame)),
	}

	data := append(header, frame...)

	// Minimal ADTS header for framing detection.
	return append(data, 0xff, 0xf1, 0x50, 0x80, 0x00, 0xff, 0xfc)
}

func TestInspectHLSSegmentContainers(t *testing.T) {
	ts := make([]byte, 188)
	ts[0] = 0x47

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"mpegts", ts, ".ts"},
		{"packed_aac", packedAACFixture(), ".aac"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "segment")
			if err := os.WriteFile(path, tc.data, 0600); err != nil {
				t.Fatal(err)
			}

			got, err := inspectHLSSegment(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("extension = %q; want %q", got, tc.want)
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(tc.data) {
				t.Fatal("inspection modified the segment")
			}
		})
	}
}

func TestInspectHLSSegmentRejectsInvalidData(t *testing.T) {
	truncatedTS := make([]byte, 189)
	truncatedTS[0] = 0x47

	badSize := packedAACFixture()
	badSize[6] = 0x80

	badADTS := packedAACFixture()
	badADTS[len(badADTS)-7] = 0

	missingOwner := packedAACFixture()
	missingOwner[20] = 'X'

	flaggedID3 := packedAACFixture()
	flaggedID3[5] = 0x80

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"html", []byte("<html>error response</html>")},
		{"truncated_ts", truncatedTS},
		{"invalid_id3_size", badSize},
		{"missing_adts", badADTS},
		{"missing_timestamp_owner", missingOwner},
		{"unsupported_id3_flags", flaggedID3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "segment")
			if err := os.WriteFile(path, tc.data, 0600); err != nil {
				t.Fatal(err)
			}

			if _, err := inspectHLSSegment(
				context.Background(), path,
			); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
