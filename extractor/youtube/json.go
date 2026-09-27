package youtube

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// SchemaVersion is the current inspect JSON schema version.
const SchemaVersion = 1

// InspectResponse is the top-level versioned JSON document for goyt inspect.
type InspectResponse struct {
	SchemaVersion int                   `json:"schema_version"`
	Results       []ClientInspectResult `json:"results"`
}

// ClientInspectResult holds the inspection outcome for a specific YouTube client profile.
type ClientInspectResult struct {
	Client                string                   `json:"client"`
	Status                string                   `json:"status"`
	Media                 *InspectMediaDTO         `json:"media"`
	Playback              *InspectPlaybackDTO      `json:"playback"`
	Streaming             *InspectStreamingDTO     `json:"streaming"`
	AvailableVideoHeights []int                    `json:"available_video_heights"`
	Formats               []InspectFormatDTO       `json:"formats"`
	AudioTracks           []InspectAudioTrackDTO   `json:"audio_tracks"`
	HLSAudioRenditions    []InspectHLSRenditionDTO `json:"hls_audio_renditions"`
	Limitations           []string                 `json:"limitations"`
	Error                 *InspectErrorDTO         `json:"error"`
}

// InspectMediaDTO contains basic video metadata.
type InspectMediaDTO struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	DurationSeconds *float64 `json:"duration_seconds"`
}

// InspectPlaybackDTO reports playback playability status and reason.
type InspectPlaybackDTO struct {
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}

// InspectStreamingDTO indicates presence of streaming manifest/endpoint types.
type InspectStreamingDTO struct {
	HLS  bool `json:"hls"`
	DASH bool `json:"dash"`
	SABR bool `json:"sabr"`
}

// InspectFormatDTO contains safe, allowlisted metadata for a discovered format.
type InspectFormatDTO struct {
	ID                 int     `json:"id"`
	MIMEType           string  `json:"mime_type"`
	Codecs             *string `json:"codecs"`
	Quality            *string `json:"quality"`
	Width              *int    `json:"width"`
	Height             *int    `json:"height"`
	Bitrate            *int64  `json:"bitrate"`
	HasDirectURL       bool    `json:"has_direct_url"`
	SignatureChallenge bool    `json:"signature_challenge"`
	NChallenge         bool    `json:"n_challenge"`
	DRMReported        bool    `json:"drm_reported"`
}

// InspectAudioTrackDTO represents audio track information from the YouTube player API.
type InspectAudioTrackDTO struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Language     *string `json:"language"`
	Default      bool    `json:"default"`
	OriginalHint bool    `json:"original_hint"`
}

// InspectHLSRenditionDTO represents an audio rendition from an HLS master playlist.
type InspectHLSRenditionDTO struct {
	GroupID      string  `json:"group_id"`
	Name         string  `json:"name"`
	Language     *string `json:"language"`
	Default      bool    `json:"default"`
	AutoSelect   bool    `json:"autoselect"`
	OriginalHint bool    `json:"original_hint"`
}

// InspectErrorDTO provides a structured error code and message.
type InspectErrorDTO struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// BuildInspectResponse constructs an InspectResponse wrapping the given client results.
func BuildInspectResponse(results []ClientInspectResult) InspectResponse {
	if results == nil {
		results = make([]ClientInspectResult, 0)
	}
	return InspectResponse{
		SchemaVersion: SchemaVersion,
		Results:       results,
	}
}

// BuildClientInspectResult converts an extraction Report or error into a ClientInspectResult.
func BuildClientInspectResult(clientName string, report *Report, err error) ClientInspectResult {
	if err != nil {
		code := "extraction_failed"
		switch {
		case errors.Is(err, context.Canceled):
			code = "context_canceled"
		case errors.Is(err, context.DeadlineExceeded):
			code = "timeout"
		case errors.Is(err, ErrPlayerResponseMissing):
			code = "player_response_missing"
		}

		// Ensure error message does not contain sensitive details and is sanitized
		msg := err.Error()

		return ClientInspectResult{
			Client:                clientName,
			Status:                "error",
			Media:                 nil,
			Playback:              nil,
			Streaming:             nil,
			AvailableVideoHeights: make([]int, 0),
			Formats:               make([]InspectFormatDTO, 0),
			AudioTracks:           make([]InspectAudioTrackDTO, 0),
			HLSAudioRenditions:    make([]InspectHLSRenditionDTO, 0),
			Limitations:           make([]string, 0),
			Error: &InspectErrorDTO{
				Code:    code,
				Message: msg,
			},
		}
	}

	var mediaDTO *InspectMediaDTO
	if report != nil && report.Media != nil {
		var durSec *float64
		if report.Media.Duration != nil {
			sec := report.Media.Duration.Seconds()
			durSec = &sec
		}
		mediaDTO = &InspectMediaDTO{
			ID:              report.Media.ID,
			Title:           report.Media.Title,
			DurationSeconds: durSec,
		}
	}

	var playbackDTO *InspectPlaybackDTO
	if report != nil {
		var reason *string
		if report.PlaybackReason != "" {
			r := report.PlaybackReason
			reason = &r
		}
		playbackDTO = &InspectPlaybackDTO{
			Status: report.PlaybackStatus,
			Reason: reason,
		}
	}

	var streamingDTO *InspectStreamingDTO
	if report != nil {
		streamingDTO = &InspectStreamingDTO{
			HLS:  report.HasHLSManifest,
			DASH: report.HasDASHManifest,
			SABR: report.HasSABREndpoint,
		}
	}

	heightsMap := make(map[int]struct{})
	var formatsDTO []InspectFormatDTO
	if report != nil && len(report.Formats) > 0 {
		formatsDTO = make([]InspectFormatDTO, 0, len(report.Formats))
		for _, f := range report.Formats {
			if f.Height > 0 {
				heightsMap[f.Height] = struct{}{}
			}

			var codecs *string
			if f.Codecs != "" {
				c := f.Codecs
				codecs = &c
			}

			var quality *string
			if f.Quality != "" {
				q := f.Quality
				quality = &q
			}

			var width *int
			if f.Width > 0 {
				w := f.Width
				width = &w
			}

			var height *int
			if f.Height > 0 {
				h := f.Height
				height = &h
			}

			var bitrate *int64
			if f.Bitrate > 0 {
				b := f.Bitrate
				bitrate = &b
			}

			formatsDTO = append(formatsDTO, InspectFormatDTO{
				ID:                 f.ID,
				MIMEType:           f.MIMEType,
				Codecs:             codecs,
				Quality:            quality,
				Width:              width,
				Height:             height,
				Bitrate:            bitrate,
				HasDirectURL:       f.HasDirectURL,
				SignatureChallenge: f.SignatureChallenge,
				NChallenge:         f.NChallenge,
				DRMReported:        f.DRMReported,
			})
		}
	} else {
		formatsDTO = make([]InspectFormatDTO, 0)
	}

	heights := make([]int, 0, len(heightsMap))
	for h := range heightsMap {
		heights = append(heights, h)
	}
	sort.Ints(heights)

	var audioTracksDTO []InspectAudioTrackDTO
	if report != nil && len(report.AudioTracks) > 0 {
		audioTracksDTO = make([]InspectAudioTrackDTO, 0, len(report.AudioTracks))
		for _, at := range report.AudioTracks {
			audioTracksDTO = append(audioTracksDTO, InspectAudioTrackDTO{
				ID:           at.ID,
				Name:         at.DisplayName,
				Language:     nil, // YouTube player audio tracks do not expose an explicit language field
				Default:      at.AudioIsDefault,
				OriginalHint: at.IsOriginal,
			})
		}
	} else {
		audioTracksDTO = make([]InspectAudioTrackDTO, 0)
	}

	var hlsRenditionsDTO []InspectHLSRenditionDTO
	if report != nil && len(report.HLSRenditions) > 0 {
		hlsRenditionsDTO = make([]InspectHLSRenditionDTO, 0, len(report.HLSRenditions))
		for _, hr := range report.HLSRenditions {
			var lang *string
			if strings.TrimSpace(hr.Language) != "" {
				l := hr.Language
				lang = &l
			}
			hlsRenditionsDTO = append(hlsRenditionsDTO, InspectHLSRenditionDTO{
				GroupID:      hr.GroupID,
				Name:         hr.Name,
				Language:     lang,
				Default:      hr.Default,
				AutoSelect:   hr.AutoSelect,
				OriginalHint: hr.IsOriginal,
			})
		}
	} else {
		hlsRenditionsDTO = make([]InspectHLSRenditionDTO, 0)
	}

	limitations := make([]string, 0)
	if report != nil && len(report.Limitations) > 0 {
		limitations = append(limitations, report.Limitations...)
	}

	return ClientInspectResult{
		Client:                clientName,
		Status:                "ok",
		Media:                 mediaDTO,
		Playback:              playbackDTO,
		Streaming:             streamingDTO,
		AvailableVideoHeights: heights,
		Formats:               formatsDTO,
		AudioTracks:           audioTracksDTO,
		HLSAudioRenditions:    hlsRenditionsDTO,
		Limitations:           limitations,
		Error:                 nil,
	}
}
