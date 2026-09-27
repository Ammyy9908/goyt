package goyt

import (
	"errors"
	"net/url"
	"strings"
)

// ErrNoMatchingFormats means no available combination satisfies the request.
var ErrNoMatchingFormats = errors.New(
	"goyt: no formats satisfy the selection requirements",
)

// Selection describes requirements for an audio/video output.
//
// Empty strings impose no constraint.
// MaxHeight == 0 means no height limit.
//
// Language matches exactly, ignoring case: "en" does not match "en-US".
//
// VideoCodec and AudioCodec accept the normalized codec names supported below,
// as well as recognized codec identifiers such as avc1.640028 or mp4a.40.2.
type Selection struct {
	MaxHeight     int
	AudioLanguage string
	VideoCodec    string
	AudioCodec    string
	Container     string
	AllowSeparate bool
}

func normalizeCodec(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))

	switch {
	case value == "h264",
		value == "avc1",
		strings.HasPrefix(value, "avc1."),
		value == "avc3",
		strings.HasPrefix(value, "avc3."):
		return "h264"

	case value == "aac",
		value == "mp4a.40.2",
		value == "mp4a.40.5",
		value == "mp4a.40.29":
		return "aac"

	case value == "vp8",
		value == "vp08",
		strings.HasPrefix(value, "vp08."):
		return "vp8"

	case value == "vp9",
		value == "vp09",
		strings.HasPrefix(value, "vp09."):
		return "vp9"

	case value == "av1",
		value == "av01",
		strings.HasPrefix(value, "av01."):
		return "av1"

	default:
		return value
	}
}

func normalizeContainer(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))

	if value == "matroska" {
		return "mkv"
	}

	return value
}

func knownVideo(codec string) bool {
	switch normalizeCodec(codec) {
	case "h264", "vp8", "vp9", "av1":
		return true
	default:
		return false
	}
}

func knownAudio(codec string) bool {
	switch normalizeCodec(codec) {
	case "aac", "opus", "vorbis":
		return true
	default:
		return false
	}
}

// This intentionally models a small compatibility subset.
// Expand it with explicit tests when adding more codecs.
func compatible(container, video, audio string) bool {
	video = normalizeCodec(video)
	audio = normalizeCodec(audio)

	switch normalizeContainer(container) {
	case "mp4":
		return video == "h264" && audio == "aac"

	case "webm":
		return (video == "vp8" || video == "vp9" || video == "av1") &&
			(audio == "opus" || audio == "vorbis")

	case "mkv":
		return knownVideo(video) && knownAudio(audio)

	default:
		return false
	}
}

func normalizeSelection(s Selection) (Selection, error) {
	if s.MaxHeight < 0 {
		return Selection{}, errors.New("goyt: MaxHeight cannot be negative")
	}

	s.VideoCodec = normalizeCodec(s.VideoCodec)
	s.AudioCodec = normalizeCodec(s.AudioCodec)
	s.Container = normalizeContainer(s.Container)
	s.AudioLanguage = strings.ToLower(strings.TrimSpace(s.AudioLanguage))

	if s.VideoCodec != "" && !knownVideo(s.VideoCodec) {
		return Selection{}, errors.New("goyt: unsupported video codec requirement")
	}

	if s.AudioCodec != "" && !knownAudio(s.AudioCodec) {
		return Selection{}, errors.New("goyt: unsupported audio codec requirement")
	}

	switch s.Container {
	case "", "mp4", "webm", "mkv":
	default:
		return Selection{}, errors.New("goyt: unsupported output container")
	}

	return s, nil
}

func usableResource(f Format) bool {
	switch f.Protocol {
	case ProtocolHTTP, ProtocolHLS, ProtocolDASH:
	default:
		return false
	}

	u, err := url.Parse(f.Resource.URL)

	return err == nil &&
		u.Hostname() != "" &&
		u.User == nil &&
		(u.Scheme == "http" || u.Scheme == "https")
}

func matchesVideo(f Format, s Selection) bool {
	codec := normalizeCodec(f.VideoCodec)

	if !knownVideo(codec) {
		return false
	}

	if s.VideoCodec != "" && codec != s.VideoCodec {
		return false
	}

	if f.Height != nil && *f.Height <= 0 {
		return false
	}

	// Unknown height cannot satisfy a strict height limit.
	if s.MaxHeight > 0 &&
		(f.Height == nil || *f.Height > s.MaxHeight) {
		return false
	}

	return true
}

func matchesAudio(f Format, s Selection) bool {
	codec := normalizeCodec(f.AudioCodec)

	if !knownAudio(codec) {
		return false
	}

	if s.AudioCodec != "" && codec != s.AudioCodec {
		return false
	}

	language := strings.ToLower(strings.TrimSpace(f.Language))

	return s.AudioLanguage == "" || language == s.AudioLanguage
}
