package goyt

import (
	"errors"
	"strings"
)

// DownloadPlan describes the selected inputs and required processing.
//
// Streams contains either:
//   - one combined audio/video format; or
//   - video-only first, followed by audio-only.
//
// This is a plan, not an execution result. HLS, DASH, merging, and remuxing
// require capabilities that will be implemented in later phases.
type DownloadPlan struct {
	MediaID         string
	Title           string
	Streams         []Format
	OutputContainer string
	NeedsMerge      bool
	NeedsRemux      bool
}

// Plan selects audio/video formats without performing I/O.
//
// It does not mutate media. Returned stream metadata is copied so callers can
// inspect or modify the plan without changing the original extraction result.
//
// Metadata-only results and audio-only requests are not supported by this
// initial planner.
func Plan(media *Media, selection Selection) (*DownloadPlan, error) {
	if media == nil {
		return nil, errors.New("goyt: media is nil")
	}

	s, err := normalizeSelection(selection)
	if err != nil {
		return nil, err
	}

	var best *DownloadPlan

	consider := func(streams []Format, video, audio string) {
		container := chooseContainer(streams, video, audio, s.Container)
		if container == "" {
			return
		}

		candidate := &DownloadPlan{
			MediaID:         media.ID,
			Title:           media.Title,
			Streams:         streams,
			OutputContainer: container,
			NeedsMerge:      len(streams) == 2,
		}

		// For one combined input, a different output container means remuxing.
		// Separate inputs already require a merge into the selected container.
		candidate.NeedsRemux = len(streams) == 1 &&
			normalizeContainer(streams[0].Container) != container

		if betterPlan(candidate, best) {
			best = candidate
		}
	}

	// Combined audio/video candidates.
	for _, f := range media.Formats {
		if !usableResource(f) ||
			!matchesVideo(f, s) ||
			!matchesAudio(f, s) {
			continue
		}

		consider(
			[]Format{f},
			f.VideoCodec,
			f.AudioCodec,
		)
	}

	// Separate video/audio candidates.
	if s.AllowSeparate {
		for _, video := range media.Formats {
			if !usableResource(video) ||
				!matchesVideo(video, s) ||
				normalizeCodec(video.AudioCodec) != "none" {
				continue
			}

			for _, audio := range media.Formats {
				if !usableResource(audio) ||
					!matchesAudio(audio, s) ||
					normalizeCodec(audio.VideoCodec) != "none" {
					continue
				}

				consider(
					[]Format{video, audio},
					video.VideoCodec,
					audio.AudioCodec,
				)
			}
		}
	}

	if best == nil {
		return nil, ErrNoMatchingFormats
	}

	for i := range best.Streams {
		best.Streams[i] = cloneFormat(best.Streams[i])
	}

	return best, nil
}

func chooseContainer(
	streams []Format,
	video string,
	audio string,
	requested string,
) string {
	if requested != "" {
		if compatible(requested, video, audio) {
			return requested
		}
		return ""
	}

	// Preserve a compatible combined input container where possible.
	if len(streams) == 1 {
		source := normalizeContainer(streams[0].Container)
		if compatible(source, video, audio) {
			return source
		}
	}

	// Deterministic defaults for separate inputs or unsupported source containers.
	for _, container := range []string{"mp4", "webm", "mkv"} {
		if compatible(container, video, audio) {
			return container
		}
	}

	return ""
}

func betterPlan(candidate, current *DownloadPlan) bool {
	if current == nil {
		return true
	}

	candidateHeight := formatHeight(candidate.Streams[0])
	currentHeight := formatHeight(current.Streams[0])

	if candidateHeight != currentHeight {
		return candidateHeight > currentHeight
	}

	if candidate.NeedsMerge != current.NeedsMerge {
		return !candidate.NeedsMerge
	}

	candidateRate := formatBitrate(candidate.Streams[0])
	currentRate := formatBitrate(current.Streams[0])

	if candidateRate != currentRate {
		return candidateRate > currentRate
	}

	if candidate.NeedsMerge {
		if better, decided := betterAudio(candidate.Streams[1], current.Streams[1]); decided {
			return better
		}
	} else {
		if better, decided := betterAudio(candidate.Streams[0], current.Streams[0]); decided {
			return better
		}
	}

	// Preserve the first candidate when all ranking factors are equal.
	return false
}

// betterAudio compares two audio formats using deterministic criteria:
// 1. AudioIsOriginal preference.
// 2. AudioIsDefault preference.
// 3. Audio codec rank (Opus > AAC). Different audio codecs are never ranked solely by bitrate.
// 4. Bitrate comparison within the same audio codec.
func betterAudio(candidate, current Format) (better bool, decided bool) {
	if candidate.AudioIsOriginal != current.AudioIsOriginal {
		return candidate.AudioIsOriginal, true
	}

	if candidate.AudioIsDefault != current.AudioIsDefault {
		return candidate.AudioIsDefault, true
	}

	candCodec := normalizeCodec(candidate.AudioCodec)
	currCodec := normalizeCodec(current.AudioCodec)

	if candCodec != currCodec {
		candRank := audioCodecRank(candCodec)
		currRank := audioCodecRank(currCodec)
		if candRank != currRank {
			return candRank > currRank, true
		}
	}

	candRate := formatBitrate(candidate)
	currRate := formatBitrate(current)

	if candRate != currRate {
		return candRate > currRate, true
	}

	return false, false
}

// audioCodecRank provides a deterministic ranking across different audio codecs.
// Opus is ranked above AAC due to higher encoding efficiency at equal bitrates.
func audioCodecRank(codec string) int {
	switch normalizeCodec(codec) {
	case "opus":
		return 2
	case "aac":
		return 1
	default:
		return 0
	}
}

func formatHeight(f Format) int {
	if f.Height == nil {
		return -1
	}
	return *f.Height
}

func formatBitrate(f Format) int64 {
	if f.Bitrate == nil || *f.Bitrate < 0 {
		return -1
	}
	return *f.Bitrate
}

func cloneValue[T any](value *T) *T {
	if value == nil {
		return nil
	}

	copy := *value
	return &copy
}

func cloneFormat(f Format) Format {
	f.Width = cloneValue(f.Width)
	f.Height = cloneValue(f.Height)
	f.Bitrate = cloneValue(f.Bitrate)
	f.SizeBytes = cloneValue(f.SizeBytes)
	f.Resource.ExpiresAt = cloneValue(f.Resource.ExpiresAt)
	f.Resource.Headers = f.Resource.Headers.Clone()

	return f
}

// DirectDownloadable reports whether Phase 2 can execute this plan as one
// direct HTTP file transfer.
//
// It does not verify that the resource URL is still valid or unexpired.
func (p *DownloadPlan) DirectDownloadable() bool {
	return p != nil &&
		len(p.Streams) == 1 &&
		p.Streams[0].Protocol == ProtocolHTTP &&
		!p.NeedsMerge &&
		!p.NeedsRemux
}

// Extension returns the planned output extension, without a leading dot.
func (p *DownloadPlan) Extension() string {
	if p == nil {
		return ""
	}

	return strings.TrimSpace(p.OutputContainer)
}
