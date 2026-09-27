package goyt

import (
	"errors"
	"fmt"
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

// Video Codec constants
const (
	VideoCodecH264 = "h264"
	VideoCodecVP9  = "vp9"
	VideoCodecAV1  = "av1"
)

// Container constants
const (
	ContainerMP4  = "mp4"
	ContainerWebM = "webm"
	ContainerMKV  = "mkv"
)

// SupportedVideoCodecs returns the list of video codecs supported by goyt.
func SupportedVideoCodecs() []string {
	return []string{VideoCodecH264, VideoCodecVP9, VideoCodecAV1}
}

// SupportedVideoContainers returns the list of output containers supported for video.
func SupportedVideoContainers() []string {
	return []string{ContainerMP4, ContainerWebM, ContainerMKV}
}

// NormalizeVideoCodec normalizes a video codec string or family name (e.g. avc1, vp09, av01)
// to its canonical name ("h264", "vp9", "av1").
func NormalizeVideoCodec(value string) (string, error) {
	norm := normalizeCodec(value)
	switch norm {
	case VideoCodecH264, VideoCodecVP9, VideoCodecAV1:
		return norm, nil
	default:
		return "", fmt.Errorf("unsupported video codec %q; supported codecs are %s", value, strings.Join(SupportedVideoCodecs(), ", "))
	}
}

// NormalizeAudioCodec normalizes an audio codec string to canonical names ("aac", "opus").
func NormalizeAudioCodec(value string) (string, error) {
	norm := normalizeCodec(value)
	switch norm {
	case "aac", "opus":
		return norm, nil
	default:
		return "", fmt.Errorf("unsupported audio codec %q; supported codecs are aac, opus", value)
	}
}

// NormalizeContainer normalizes an output container name or file extension (e.g. .mp4, matroska)
// to its canonical name ("mp4", "webm", "mkv").
func NormalizeContainer(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, ".")

	if value == "matroska" {
		return ContainerMKV, nil
	}

	switch value {
	case ContainerMP4, ContainerWebM, ContainerMKV:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported container %q; supported containers are %s", value, strings.Join(SupportedVideoContainers(), ", "))
	}
}

// ValidateVideoCodecContainer validates that a video codec and container combination is supported by goyt's policy.
func ValidateVideoCodecContainer(videoCodec, container string) error {
	v, err := NormalizeVideoCodec(videoCodec)
	if err != nil {
		return err
	}
	c, err := NormalizeContainer(container)
	if err != nil {
		return err
	}

	switch c {
	case ContainerMP4:
		if v != VideoCodecH264 && v != VideoCodecAV1 {
			return fmt.Errorf("incompatible video codec %q for container %q (supported: h264, av1 for mp4)", videoCodec, container)
		}
	case ContainerWebM:
		if v != VideoCodecVP9 && v != VideoCodecAV1 {
			return fmt.Errorf("incompatible video codec %q for container %q (supported: vp9, av1 for webm)", videoCodec, container)
		}
	case ContainerMKV:
		if v != VideoCodecH264 && v != VideoCodecVP9 && v != VideoCodecAV1 {
			return fmt.Errorf("incompatible video codec %q for container %q (supported: h264, vp9, av1 for mkv)", videoCodec, container)
		}
	}
	return nil
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
		value == "mp4a.40.29",
		value == "mp4a.40.1",
		value == "mp4a.40.3",
		value == "mp4a.40.4",
		value == "mp4a.40.6",
		value == "mp4a.66",
		value == "mp4a.67",
		value == "mp4a.68":
		return "aac"

	case value == "opus":
		return "opus"

	case value == "vp8",
		value == "vp08",
		strings.HasPrefix(value, "vp08."):
		return "vp8"

	case value == "vp9",
		value == "vp09",
		strings.HasPrefix(value, "vp09."),
		strings.HasPrefix(value, "vp9."):
		return "vp9"

	case value == "av1",
		value == "av01",
		strings.HasPrefix(value, "av01."),
		strings.HasPrefix(value, "av1."):
		return "av1"

	default:
		return value
	}
}

func normalizeContainer(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, ".")

	if value == "matroska" {
		return "mkv"
	}

	return value
}

func knownVideo(codec string) bool {
	switch normalizeCodec(codec) {
	case "h264", "vp9", "av1":
		return true
	default:
		return false
	}
}

func knownAudio(codec string) bool {
	switch normalizeCodec(codec) {
	case "aac", "opus":
		return true
	default:
		return false
	}
}

// compatible checks goyt's centralized supported codec/container compatibility policy:
// - MP4: H.264 or AV1 video with AAC audio.
// - WebM: VP9 or AV1 video with Opus audio.
// - MKV: H.264, VP9, or AV1 video with AAC or Opus audio.
func compatible(container, video, audio string) bool {
	v := normalizeCodec(video)
	a := normalizeCodec(audio)
	c := normalizeContainer(container)

	switch c {
	case "mp4":
		return (v == "h264" || v == "av1") && a == "aac"

	case "webm":
		return (v == "vp9" || v == "av1") && a == "opus"

	case "mkv":
		return (v == "h264" || v == "vp9" || v == "av1") && (a == "aac" || a == "opus")

	default:
		return false
	}
}

func normalizeSelection(s Selection) (Selection, error) {
	if s.MaxHeight < 0 {
		return Selection{}, errors.New("goyt: MaxHeight cannot be negative")
	}

	if s.VideoCodec != "" {
		normVC, err := NormalizeVideoCodec(s.VideoCodec)
		if err != nil {
			return Selection{}, errors.New("goyt: unsupported video codec requirement")
		}
		s.VideoCodec = normVC
	}

	if s.AudioCodec != "" {
		normAC, err := NormalizeAudioCodec(s.AudioCodec)
		if err != nil {
			return Selection{}, errors.New("goyt: unsupported audio codec requirement")
		}
		s.AudioCodec = normAC
	}

	if s.Container != "" {
		normC, err := NormalizeContainer(s.Container)
		if err != nil {
			return Selection{}, errors.New("goyt: unsupported output container")
		}
		s.Container = normC
	}

	s.AudioLanguage = strings.ToLower(strings.TrimSpace(s.AudioLanguage))

	if s.VideoCodec != "" && s.Container != "" {
		if err := ValidateVideoCodecContainer(s.VideoCodec, s.Container); err != nil {
			return Selection{}, err
		}
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
