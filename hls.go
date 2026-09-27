package goyt

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxHLSPlaylistBytes = 16 * 1024 * 1024

type HLSSegment struct {
	URL      string
	Duration time.Duration
}

type HLSPlaylist struct {
	Segments []HLSSegment
	Duration time.Duration
}

// ParseHLS parses a deliberately limited subset of completed media playlists.
//
// base must be the final playlist URL after redirects.
// Relative segment URLs are resolved against it without manually copying
// query parameters from the playlist URL.
func ParseHLS(data []byte, base *url.URL) (*HLSPlaylist, error) {
	if len(data) > maxHLSPlaylistBytes {
		return nil, errors.New("goyt: HLS playlist exceeds size limit")
	}

	if !utf8.Valid(data) || bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return nil, errors.New("goyt: HLS playlist must be UTF-8 without BOM")
	}

	if !validHLSURL(base) {
		return nil, errors.New("goyt: invalid HLS base URL")
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maxHLSPlaylistBytes)

	if !scanner.Scan() || strings.TrimSuffix(scanner.Text(), "\r") != "#EXTM3U" {
		return nil, errors.New("goyt: missing EXTM3U header")
	}

	playlist := &HLSPlaylist{}
	var pending *time.Duration
	var target int64
	ended := false
	targetSeen := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		tag, value, _ := strings.Cut(line, ":")

		if strings.HasPrefix(line, "#") {
			switch tag {
			case "#EXTINF":
				if ended || pending != nil {
					return nil, errors.New("goyt: misplaced EXTINF")
				}

				number, _, found := strings.Cut(value, ",")
				seconds, err := strconv.ParseFloat(number, 64)

				if !found || err != nil ||
					math.IsNaN(seconds) || math.IsInf(seconds, 0) ||
					seconds <= 0 || seconds > 86400 {
					return nil, errors.New("goyt: unsupported segment duration")
				}

				duration := time.Duration(seconds * float64(time.Second))
				if duration <= 0 {
					return nil, errors.New("goyt: segment duration is too small")
				}
				pending = &duration

			case "#EXT-X-TARGETDURATION":
				if targetSeen {
					return nil, errors.New("goyt: duplicate target duration")
				}

				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil || n <= 0 || n > 86400 {
					return nil, errors.New("goyt: invalid target duration")
				}

				target = n
				targetSeen = true

			case "#EXT-X-ENDLIST":
				if ended || pending != nil || line != tag {
					return nil, errors.New("goyt: invalid ENDLIST")
				}
				ended = true

			case "#EXT-X-PLAYLIST-TYPE":
				if value != "VOD" && value != "EVENT" {
					return nil, errors.New("goyt: invalid playlist type")
				}

			case "#EXT-X-VERSION":
				n, err := strconv.Atoi(value)
				if err != nil || n < 1 || n > 7 {
					return nil, errors.New("goyt: unsupported HLS version")
				}

			case "#EXT-X-MEDIA-SEQUENCE":
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil || n < 0 {
					return nil, errors.New("goyt: invalid media sequence")
				}

			case "#EXT-X-KEY":
				if value != "METHOD=NONE" {
					return nil, errors.New("goyt: encrypted HLS is not supported yet")
				}

			case "#EXT-X-INDEPENDENT-SEGMENTS",
				"#EXT-X-PROGRAM-DATE-TIME":
				// These tags do not change this subset's download ordering.

			default:
				if strings.HasPrefix(tag, "#EXT") {
					return nil, fmt.Errorf(
						"goyt: HLS feature not supported yet: %s",
						tag,
					)
				}
				// Ordinary comments are ignored.
			}

			continue
		}

		if ended || pending == nil {
			return nil, errors.New("goyt: unexpected segment URI")
		}

		reference, err := url.Parse(line)
		if err != nil {
			return nil, errors.New("goyt: invalid segment URI")
		}

		resolved := base.ResolveReference(reference)
		if !validHLSURL(resolved) ||
			(base.Scheme == "https" && resolved.Scheme != "https") {
			return nil, errors.New("goyt: unsupported segment URL")
		}

		if len(playlist.Segments) >= 10000 {
			return nil, errors.New("goyt: HLS segment limit exceeded")
		}

		playlist.Segments = append(playlist.Segments, HLSSegment{
			URL:      resolved.String(),
			Duration: *pending,
		})
		playlist.Duration += *pending
		pending = nil
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if !ended || !targetSeen || pending != nil ||
		len(playlist.Segments) == 0 {
		return nil, errors.New(
			"goyt: expected a complete media playlist with segments, target duration, and ENDLIST",
		)
	}

	for _, segment := range playlist.Segments {
		if math.Round(segment.Duration.Seconds()) > float64(target) {
			return nil, errors.New("goyt: segment exceeds target duration")
		}
	}

	return playlist, nil
}

func validHLSURL(u *url.URL) bool {
	return u != nil &&
		(u.Scheme == "http" || u.Scheme == "https") &&
		u.Hostname() != "" &&
		u.User == nil &&
		u.Fragment == ""
}
