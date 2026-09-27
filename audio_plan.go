package goyt

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Supported audio formats for audio-only downloads.
const (
	AudioFormatBest   = "best"
	AudioFormatAAC    = "aac"
	AudioFormatALAC   = "alac"
	AudioFormatFLAC   = "flac"
	AudioFormatM4A    = "m4a"
	AudioFormatMP3    = "mp3"
	AudioFormatOpus   = "opus"
	AudioFormatVorbis = "vorbis"
	AudioFormatWAV    = "wav"
)

// SupportedAudioFormats lists all supported -audio-format values.
var SupportedAudioFormats = []string{
	AudioFormatBest,
	AudioFormatAAC,
	AudioFormatALAC,
	AudioFormatFLAC,
	AudioFormatM4A,
	AudioFormatMP3,
	AudioFormatOpus,
	AudioFormatVorbis,
	AudioFormatWAV,
}

// ErrNoAudioFormats means no audio-only format satisfies the selection requirements.
var ErrNoAudioFormats = errors.New(
	"goyt: no audio-only formats satisfy the selection requirements",
)

// ErrMuxedOnlySource means only muxed audio/video formats are available.
var ErrMuxedOnlySource = errors.New(
	"goyt: no supported audio-only streams available; muxed audio/video is not supported in audio-only mode",
)

// AudioOutputSpec defines the complete specifications for producing an audio file.
type AudioOutputSpec struct {
	RequestedFormat string // e.g. "best", "mp3", "m4a", etc.
	ResolvedCodec   string // e.g. "mp3", "aac", "opus", "pcm_s16le", etc.
	Container       string // FFmpeg output muxer name: "adts", "ipod", "flac", "mp3", "opus", "ogg", "wav"
	Extension       string // File extension including leading dot: ".aac", ".m4a", ".flac", ".mp3", ".opus", ".ogg", ".wav"
	Copy            bool   // True if stream copying (-c:a copy), false if encoding
	Encoder         string // FFmpeg audio encoder: "aac", "alac", "flac", "libmp3lame", "libopus", "libvorbis", "pcm_s16le"
	Quality         int    // MP3 VBR quality (0–9); -1 if not set
	Bitrate         string // Target bitrate string (e.g. "128k"); empty if not set
}

// AudioSelection describes requirements for an audio-only download.
type AudioSelection struct {
	AudioLanguage string
	AudioFormat   string // e.g. "best", "mp3", "m4a", etc. Default "best"
	AudioQuality  *int   // 0 (highest) to 9 (lowest) for MP3; nil if not set
	AudioBitrate  string // e.g. "128k"; empty if not set
	AudioCodec    string // optional filter, e.g. "aac", "opus"
}

// AudioPlan describes the selected audio stream and conversion parameters.
type AudioPlan struct {
	MediaID      string
	Title        string
	Stream       Format
	OutputSpec   AudioOutputSpec
	OutputFormat string // Backwards compatibility: equals OutputSpec.RequestedFormat
	Quality      int    // Backwards compatibility: equals OutputSpec.Quality (or 2)
}

// ExpectedExtensionForFormat returns the canonical file extension for a fixed audio format.
// For "best", it returns ("", false) because the extension depends on the source codec.
func ExpectedExtensionForFormat(format string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case AudioFormatAAC:
		return ".aac", true
	case AudioFormatALAC:
		return ".m4a", true
	case AudioFormatFLAC:
		return ".flac", true
	case AudioFormatM4A:
		return ".m4a", true
	case AudioFormatMP3:
		return ".mp3", true
	case AudioFormatOpus:
		return ".opus", true
	case AudioFormatVorbis:
		return ".ogg", true
	case AudioFormatWAV:
		return ".wav", true
	default:
		return "", false
	}
}

// ParseAudioBitrate validates and normalizes an audio bitrate string (e.g. "128k", "192000").
func ParseAudioBitrate(s string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(s))
	if raw == "" {
		return "", errors.New("goyt: audio bitrate cannot be empty")
	}

	trimmed := strings.TrimSuffix(raw, "bps")
	trimmed = strings.TrimSuffix(trimmed, "bit/s")
	trimmed = strings.TrimSuffix(trimmed, "b/s")
	trimmed = strings.TrimSuffix(trimmed, "bit")

	if strings.HasSuffix(trimmed, "k") {
		numStr := strings.TrimSuffix(trimmed, "k")
		val, err := strconv.ParseFloat(numStr, 64)
		if err != nil || val <= 0 {
			return "", fmt.Errorf("goyt: invalid audio bitrate %q: must be a positive bitrate (e.g. 128k)", s)
		}
		return trimmed, nil
	}

	if strings.HasSuffix(trimmed, "m") {
		numStr := strings.TrimSuffix(trimmed, "m")
		val, err := strconv.ParseFloat(numStr, 64)
		if err != nil || val <= 0 {
			return "", fmt.Errorf("goyt: invalid audio bitrate %q: must be a positive bitrate (e.g. 128k)", s)
		}
		return trimmed, nil
	}

	val, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || val <= 0 {
		return "", fmt.Errorf("goyt: invalid audio bitrate %q: must be a positive bitrate (e.g. 128k)", s)
	}

	return trimmed, nil
}

// ResolveAudioOutputSpec determines the target container, codec, extension, encoder,
// and copy/encode decision for a given requested format and source audio codec.
func ResolveAudioOutputSpec(
	requestedFormat string,
	sourceCodec string,
	quality *int,
	bitrate string,
) (AudioOutputSpec, error) {
	req := strings.ToLower(strings.TrimSpace(requestedFormat))
	if req == "" {
		req = AudioFormatBest
	}

	src := normalizeCodec(sourceCodec)

	// Validate quality & bitrate mutual exclusivity
	if quality != nil && bitrate != "" {
		return AudioOutputSpec{}, errors.New("goyt: explicit -audio-quality and -audio-bitrate are mutually exclusive")
	}

	if quality != nil {
		if *quality < 0 || *quality > 9 {
			return AudioOutputSpec{}, errors.New("goyt: audio quality must be between 0 and 9 (inclusive); lower values request higher quality")
		}
		if req != AudioFormatMP3 {
			return AudioOutputSpec{}, fmt.Errorf("goyt: -audio-quality is only supported for mp3 format, not %q", req)
		}
	}

	if bitrate != "" {
		normalizedBitrate, err := ParseAudioBitrate(bitrate)
		if err != nil {
			return AudioOutputSpec{}, err
		}
		bitrate = normalizedBitrate

		switch req {
		case AudioFormatAAC, AudioFormatM4A, AudioFormatMP3, AudioFormatOpus, AudioFormatVorbis:
			// Bitrate supported for lossy formats
		default:
			return AudioOutputSpec{}, fmt.Errorf("goyt: -audio-bitrate is not supported for %s format", req)
		}
	}

	spec := AudioOutputSpec{
		RequestedFormat: req,
		Quality:         -1,
		Bitrate:         bitrate,
	}

	switch req {
	case AudioFormatBest:
		if src == "" {
			return AudioOutputSpec{}, errors.New("goyt: cannot resolve best audio format without source codec")
		}
		spec.Copy = true
		spec.ResolvedCodec = src
		switch src {
		case "aac", "alac":
			spec.Container = "ipod"
			spec.Extension = ".m4a"
		case "opus":
			spec.Container = "opus"
			spec.Extension = ".opus"
		case "vorbis":
			spec.Container = "ogg"
			spec.Extension = ".ogg"
		case "flac":
			spec.Container = "flac"
			spec.Extension = ".flac"
		case "mp3":
			spec.Container = "mp3"
			spec.Extension = ".mp3"
		case "pcm_s16le", "pcm_s16be":
			spec.Container = "wav"
			spec.Extension = ".wav"
			spec.ResolvedCodec = "pcm_s16le"
		default:
			return AudioOutputSpec{}, fmt.Errorf("goyt: cannot safely copy source audio codec %q without re-encoding in best mode", src)
		}

	case AudioFormatAAC:
		spec.ResolvedCodec = "aac"
		spec.Container = "adts"
		spec.Extension = ".aac"
		if src == "aac" && bitrate == "" {
			spec.Copy = true
		} else {
			spec.Copy = false
			spec.Encoder = "aac"
		}

	case AudioFormatM4A:
		spec.ResolvedCodec = "aac"
		spec.Container = "ipod"
		spec.Extension = ".m4a"
		if src == "aac" && bitrate == "" {
			spec.Copy = true
		} else {
			spec.Copy = false
			spec.Encoder = "aac"
		}

	case AudioFormatALAC:
		spec.ResolvedCodec = "alac"
		spec.Container = "ipod"
		spec.Extension = ".m4a"
		spec.Copy = false
		spec.Encoder = "alac"

	case AudioFormatFLAC:
		spec.ResolvedCodec = "flac"
		spec.Container = "flac"
		spec.Extension = ".flac"
		if src == "flac" {
			spec.Copy = true
		} else {
			spec.Copy = false
			spec.Encoder = "flac"
		}

	case AudioFormatMP3:
		spec.ResolvedCodec = "mp3"
		spec.Container = "mp3"
		spec.Extension = ".mp3"
		if src == "mp3" && quality == nil && bitrate == "" {
			spec.Copy = true
		} else {
			spec.Copy = false
			spec.Encoder = "libmp3lame"
			if quality != nil {
				spec.Quality = *quality
			} else if bitrate != "" {
				spec.Bitrate = bitrate
			} else {
				spec.Quality = 2 // default MP3 VBR quality
			}
		}

	case AudioFormatOpus:
		spec.ResolvedCodec = "opus"
		spec.Container = "opus"
		spec.Extension = ".opus"
		if src == "opus" && bitrate == "" {
			spec.Copy = true
		} else {
			spec.Copy = false
			spec.Encoder = "libopus"
		}

	case AudioFormatVorbis:
		spec.ResolvedCodec = "vorbis"
		spec.Container = "ogg"
		spec.Extension = ".ogg"
		if src == "vorbis" && bitrate == "" {
			spec.Copy = true
		} else {
			spec.Copy = false
			spec.Encoder = "libvorbis"
		}

	case AudioFormatWAV:
		spec.ResolvedCodec = "pcm_s16le"
		spec.Container = "wav"
		spec.Extension = ".wav"
		spec.Copy = false
		spec.Encoder = "pcm_s16le"

	default:
		return AudioOutputSpec{}, fmt.Errorf("goyt: unsupported audio format %q; supported formats are best, aac, alac, flac, m4a, mp3, opus, vorbis, wav", req)
	}

	return spec, nil
}

func normalizeAudioSelection(s AudioSelection) (AudioSelection, error) {
	s.AudioLanguage = strings.ToLower(strings.TrimSpace(s.AudioLanguage))
	s.AudioFormat = strings.ToLower(strings.TrimSpace(s.AudioFormat))
	if s.AudioFormat == "" {
		s.AudioFormat = AudioFormatBest
	}

	validFormat := false
	for _, f := range SupportedAudioFormats {
		if s.AudioFormat == f {
			validFormat = true
			break
		}
	}
	if !validFormat {
		return AudioSelection{}, fmt.Errorf("goyt: unsupported audio format %q; supported formats are best, aac, alac, flac, m4a, mp3, opus, vorbis, wav", s.AudioFormat)
	}

	if s.AudioQuality != nil && s.AudioBitrate != "" {
		return AudioSelection{}, errors.New("goyt: explicit -audio-quality and -audio-bitrate are mutually exclusive")
	}

	if s.AudioQuality != nil {
		if *s.AudioQuality < 0 || *s.AudioQuality > 9 {
			return AudioSelection{}, errors.New("goyt: audio quality must be between 0 and 9 (inclusive); lower values request higher quality")
		}
		if s.AudioFormat != AudioFormatMP3 {
			return AudioSelection{}, fmt.Errorf("goyt: -audio-quality is only supported for mp3 format, not %q", s.AudioFormat)
		}
	}

	if s.AudioBitrate != "" {
		normalizedBitrate, err := ParseAudioBitrate(s.AudioBitrate)
		if err != nil {
			return AudioSelection{}, err
		}
		s.AudioBitrate = normalizedBitrate
		switch s.AudioFormat {
		case AudioFormatAAC, AudioFormatM4A, AudioFormatMP3, AudioFormatOpus, AudioFormatVorbis:
		default:
			return AudioSelection{}, fmt.Errorf("goyt: -audio-bitrate is not supported for %s format", s.AudioFormat)
		}
	}

	s.AudioCodec = normalizeCodec(s.AudioCodec)
	if s.AudioCodec != "" && !knownAudio(s.AudioCodec) {
		return AudioSelection{}, errors.New("goyt: unsupported audio codec requirement")
	}

	return s, nil
}

// PlanAudio selects a supported audio-only stream from media without downloading it
// and resolves the complete AudioOutputSpec.
func PlanAudio(media *Media, selection AudioSelection) (*AudioPlan, error) {
	if media == nil {
		return nil, errors.New("goyt: media is nil")
	}

	s, err := normalizeAudioSelection(selection)
	if err != nil {
		return nil, err
	}

	var audioOnlyCandidates []Format
	var muxedCount int

	for _, f := range media.Formats {
		if !usableResource(f) || f.Protocol != ProtocolHTTP {
			continue
		}

		isVideoNone := f.VideoCodec == "" || f.VideoCodec == "none" || normalizeCodec(f.VideoCodec) == "none"
		isAudioNone := f.AudioCodec == "" || f.AudioCodec == "none" || normalizeCodec(f.AudioCodec) == "none"

		if isVideoNone && !isAudioNone && knownAudio(f.AudioCodec) {
			audioOnlyCandidates = append(audioOnlyCandidates, f)
		} else if !isVideoNone && !isAudioNone {
			muxedCount++
		}
	}

	if len(audioOnlyCandidates) == 0 {
		if muxedCount > 0 {
			return nil, ErrMuxedOnlySource
		}
		return nil, ErrNoAudioFormats
	}

	// Filter by requested AudioCodec if specified.
	if s.AudioCodec != "" {
		var filtered []Format
		for _, f := range audioOnlyCandidates {
			if normalizeCodec(f.AudioCodec) == s.AudioCodec {
				filtered = append(filtered, f)
			}
		}
		audioOnlyCandidates = filtered
		if len(audioOnlyCandidates) == 0 {
			return nil, ErrNoAudioFormats
		}
	}

	// Language filtering and original preference.
	var matchedCandidates []Format
	if s.AudioLanguage != "" {
		bestLangRank := -1
		for _, f := range audioOnlyCandidates {
			rank := matchFormatLanguage(f, s.AudioLanguage)
			if rank < 0 {
				continue
			}
			if rank > bestLangRank {
				bestLangRank = rank
				matchedCandidates = []Format{f}
			} else if rank == bestLangRank {
				matchedCandidates = append(matchedCandidates, f)
			}
		}
		if len(matchedCandidates) == 0 {
			return nil, fmt.Errorf("goyt: no audio stream matching language %q found", s.AudioLanguage)
		}
	} else {
		// Automatic selection: prefer original audio if evidence exists.
		var originalCandidates []Format
		for _, f := range audioOnlyCandidates {
			if f.AudioIsOriginal || isOriginalAudioName(f.AudioTrackName) {
				originalCandidates = append(originalCandidates, f)
			}
		}
		if len(originalCandidates) > 0 {
			matchedCandidates = originalCandidates
		} else {
			// Fallback: prefer default audio if marked.
			var defaultCandidates []Format
			for _, f := range audioOnlyCandidates {
				if f.AudioIsDefault {
					defaultCandidates = append(defaultCandidates, f)
				}
			}
			if len(defaultCandidates) > 0 {
				matchedCandidates = defaultCandidates
			} else {
				matchedCandidates = audioOnlyCandidates
			}
		}
	}

	// Deterministic quality ranking among candidates.
	var best *Format
	for i := range matchedCandidates {
		f := matchedCandidates[i]
		if best == nil || isBetterAudioFormat(f, *best) {
			copy := f
			best = &copy
		}
	}

	if best == nil {
		return nil, ErrNoAudioFormats
	}

	spec, err := ResolveAudioOutputSpec(s.AudioFormat, best.AudioCodec, s.AudioQuality, s.AudioBitrate)
	if err != nil {
		return nil, err
	}

	qualityVal := spec.Quality
	if qualityVal < 0 {
		qualityVal = 2
	}

	return &AudioPlan{
		MediaID:      media.ID,
		Title:        media.Title,
		Stream:       cloneFormat(*best),
		OutputSpec:   spec,
		OutputFormat: spec.RequestedFormat,
		Quality:      qualityVal,
	}, nil
}

func matchFormatLanguage(f Format, requested string) int {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		return 0
	}

	// Check Language field
	if f.Language != "" {
		if rank := hlsLanguageRank(f.Language, requested); rank >= 0 {
			return rank
		}
	}

	// Check AudioTrackID (e.g. "en.4", "es-US")
	if f.AudioTrackID != "" {
		id := strings.ToLower(strings.TrimSpace(f.AudioTrackID))
		tag, _, _ := strings.Cut(id, ".")
		if rank := hlsLanguageRank(tag, requested); rank >= 0 {
			return rank
		}
	}

	// Check AudioTrackName
	if f.AudioTrackName != "" {
		name := strings.ToLower(strings.TrimSpace(f.AudioTrackName))
		if strings.HasPrefix(name, requested) {
			return 1
		}
	}

	return -1
}

func isBetterAudioFormat(candidate, current Format) bool {
	// 1. Original audio preference
	candOrig := candidate.AudioIsOriginal || isOriginalAudioName(candidate.AudioTrackName)
	currOrig := current.AudioIsOriginal || isOriginalAudioName(current.AudioTrackName)
	if candOrig != currOrig {
		return candOrig
	}

	// 2. Default audio preference
	if candidate.AudioIsDefault != current.AudioIsDefault {
		return candidate.AudioIsDefault
	}

	// 3. Bitrate ranking (higher bitrate preferred)
	candBitrate := formatBitrate(candidate)
	currBitrate := formatBitrate(current)
	if candBitrate != currBitrate && candBitrate > 0 && currBitrate > 0 {
		return candBitrate > currBitrate
	}

	// 4. Codec tier preference: Opus (2) > AAC (1) > Vorbis (0)
	candTier := audioCodecTier(candidate.AudioCodec)
	currTier := audioCodecTier(current.AudioCodec)
	if candTier != currTier {
		return candTier > currTier
	}

	// 5. Preserve first candidate on ties for deterministic behavior
	return false
}

func audioCodecTier(codec string) int {
	switch normalizeCodec(codec) {
	case "opus":
		return 2
	case "aac":
		return 1
	case "vorbis":
		return 0
	default:
		return -1
	}
}
