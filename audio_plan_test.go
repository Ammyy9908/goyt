package goyt

import (
	"errors"
	"testing"
)

func TestResolveAudioOutputSpec(t *testing.T) {
	ptr := func(v int) *int { return &v }

	tests := []struct {
		name        string
		format      string
		sourceCodec string
		quality     *int
		bitrate     string
		wantSpec    AudioOutputSpec
		wantErr     bool
	}{
		// 1. best format
		{
			name:        "best from aac",
			format:      "best",
			sourceCodec: "aac",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "best",
				ResolvedCodec:   "aac",
				Container:       "ipod",
				Extension:       ".m4a",
				Copy:            true,
				Quality:         -1,
			},
		},
		{
			name:        "best from opus",
			format:      "best",
			sourceCodec: "opus",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "best",
				ResolvedCodec:   "opus",
				Container:       "opus",
				Extension:       ".opus",
				Copy:            true,
				Quality:         -1,
			},
		},
		{
			name:        "best from vorbis",
			format:      "best",
			sourceCodec: "vorbis",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "best",
				ResolvedCodec:   "vorbis",
				Container:       "ogg",
				Extension:       ".ogg",
				Copy:            true,
				Quality:         -1,
			},
		},
		{
			name:        "best from flac",
			format:      "best",
			sourceCodec: "flac",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "best",
				ResolvedCodec:   "flac",
				Container:       "flac",
				Extension:       ".flac",
				Copy:            true,
				Quality:         -1,
			},
		},
		{
			name:        "best from mp3",
			format:      "best",
			sourceCodec: "mp3",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "best",
				ResolvedCodec:   "mp3",
				Container:       "mp3",
				Extension:       ".mp3",
				Copy:            true,
				Quality:         -1,
			},
		},
		{
			name:        "best rejects quality",
			format:      "best",
			sourceCodec: "opus",
			quality:     ptr(2),
			wantErr:     true,
		},
		{
			name:        "best rejects bitrate",
			format:      "best",
			sourceCodec: "opus",
			bitrate:     "128k",
			wantErr:     true,
		},

		// 2. aac format
		{
			name:        "aac copy from aac",
			format:      "aac",
			sourceCodec: "aac",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "aac",
				ResolvedCodec:   "aac",
				Container:       "adts",
				Extension:       ".aac",
				Copy:            true,
				Quality:         -1,
			},
		},
		{
			name:        "aac encode from opus",
			format:      "aac",
			sourceCodec: "opus",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "aac",
				ResolvedCodec:   "aac",
				Container:       "adts",
				Extension:       ".aac",
				Copy:            false,
				Encoder:         "aac",
				Quality:         -1,
			},
		},
		{
			name:        "aac encode from aac with bitrate",
			format:      "aac",
			sourceCodec: "aac",
			bitrate:     "192k",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "aac",
				ResolvedCodec:   "aac",
				Container:       "adts",
				Extension:       ".aac",
				Copy:            false,
				Encoder:         "aac",
				Quality:         -1,
				Bitrate:         "192k",
			},
		},

		// 3. m4a format
		{
			name:        "m4a copy from aac",
			format:      "m4a",
			sourceCodec: "aac",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "m4a",
				ResolvedCodec:   "aac",
				Container:       "ipod",
				Extension:       ".m4a",
				Copy:            true,
				Quality:         -1,
			},
		},
		{
			name:        "m4a encode from opus with bitrate",
			format:      "m4a",
			sourceCodec: "opus",
			bitrate:     "256k",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "m4a",
				ResolvedCodec:   "aac",
				Container:       "ipod",
				Extension:       ".m4a",
				Copy:            false,
				Encoder:         "aac",
				Quality:         -1,
				Bitrate:         "256k",
			},
		},

		// 4. alac format
		{
			name:        "alac encode from aac",
			format:      "alac",
			sourceCodec: "aac",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "alac",
				ResolvedCodec:   "alac",
				Container:       "ipod",
				Extension:       ".m4a",
				Copy:            false,
				Encoder:         "alac",
				Quality:         -1,
			},
		},
		{
			name:        "alac rejects bitrate",
			format:      "alac",
			sourceCodec: "aac",
			bitrate:     "128k",
			wantErr:     true,
		},

		// 5. flac format
		{
			name:        "flac encode from opus",
			format:      "flac",
			sourceCodec: "opus",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "flac",
				ResolvedCodec:   "flac",
				Container:       "flac",
				Extension:       ".flac",
				Copy:            false,
				Encoder:         "flac",
				Quality:         -1,
			},
		},
		{
			name:        "flac copy from flac",
			format:      "flac",
			sourceCodec: "flac",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "flac",
				ResolvedCodec:   "flac",
				Container:       "flac",
				Extension:       ".flac",
				Copy:            true,
				Quality:         -1,
			},
		},

		// 6. mp3 format
		{
			name:        "mp3 default quality 2 from opus",
			format:      "mp3",
			sourceCodec: "opus",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "mp3",
				ResolvedCodec:   "mp3",
				Container:       "mp3",
				Extension:       ".mp3",
				Copy:            false,
				Encoder:         "libmp3lame",
				Quality:         2,
			},
		},
		{
			name:        "mp3 explicit quality 0",
			format:      "mp3",
			sourceCodec: "aac",
			quality:     ptr(0),
			wantSpec: AudioOutputSpec{
				RequestedFormat: "mp3",
				ResolvedCodec:   "mp3",
				Container:       "mp3",
				Extension:       ".mp3",
				Copy:            false,
				Encoder:         "libmp3lame",
				Quality:         0,
			},
		},
		{
			name:        "mp3 explicit bitrate 320k",
			format:      "mp3",
			sourceCodec: "aac",
			bitrate:     "320k",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "mp3",
				ResolvedCodec:   "mp3",
				Container:       "mp3",
				Extension:       ".mp3",
				Copy:            false,
				Encoder:         "libmp3lame",
				Quality:         -1,
				Bitrate:         "320k",
			},
		},
		{
			name:        "mp3 mutual exclusivity of quality and bitrate",
			format:      "mp3",
			sourceCodec: "opus",
			quality:     ptr(2),
			bitrate:     "128k",
			wantErr:     true,
		},

		// 7. opus format
		{
			name:        "opus copy from opus",
			format:      "opus",
			sourceCodec: "opus",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "opus",
				ResolvedCodec:   "opus",
				Container:       "opus",
				Extension:       ".opus",
				Copy:            true,
				Quality:         -1,
			},
		},
		{
			name:        "opus encode from aac with bitrate",
			format:      "opus",
			sourceCodec: "aac",
			bitrate:     "128k",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "opus",
				ResolvedCodec:   "opus",
				Container:       "opus",
				Extension:       ".opus",
				Copy:            false,
				Encoder:         "libopus",
				Quality:         -1,
				Bitrate:         "128k",
			},
		},

		// 8. vorbis format
		{
			name:        "vorbis copy from vorbis",
			format:      "vorbis",
			sourceCodec: "vorbis",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "vorbis",
				ResolvedCodec:   "vorbis",
				Container:       "ogg",
				Extension:       ".ogg",
				Copy:            true,
				Quality:         -1,
			},
		},
		{
			name:        "vorbis encode from aac",
			format:      "vorbis",
			sourceCodec: "aac",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "vorbis",
				ResolvedCodec:   "vorbis",
				Container:       "ogg",
				Extension:       ".ogg",
				Copy:            false,
				Encoder:         "libvorbis",
				Quality:         -1,
			},
		},

		// 9. wav format
		{
			name:        "wav encode from opus",
			format:      "wav",
			sourceCodec: "opus",
			wantSpec: AudioOutputSpec{
				RequestedFormat: "wav",
				ResolvedCodec:   "pcm_s16le",
				Container:       "wav",
				Extension:       ".wav",
				Copy:            false,
				Encoder:         "pcm_s16le",
				Quality:         -1,
			},
		},
		{
			name:        "wav rejects bitrate",
			format:      "wav",
			sourceCodec: "opus",
			bitrate:     "128k",
			wantErr:     true,
		},

		// Invalid format
		{
			name:        "unsupported format",
			format:      "wma",
			sourceCodec: "aac",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveAudioOutputSpec(tt.format, tt.sourceCodec, tt.quality, tt.bitrate)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveAudioOutputSpec() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if got != tt.wantSpec {
					t.Errorf("ResolveAudioOutputSpec() = %+v, want %+v", got, tt.wantSpec)
				}
			}
		})
	}
}

func TestExpectedExtensionForFormat(t *testing.T) {
	tests := []struct {
		format    string
		wantExt   string
		wantKnown bool
	}{
		{"aac", ".aac", true},
		{"alac", ".m4a", true},
		{"flac", ".flac", true},
		{"m4a", ".m4a", true},
		{"mp3", ".mp3", true},
		{"opus", ".opus", true},
		{"vorbis", ".ogg", true},
		{"wav", ".wav", true},
		{"best", "", false},
		{"unknown", "", false},
	}

	for _, tt := range tests {
		ext, known := ExpectedExtensionForFormat(tt.format)
		if ext != tt.wantExt || known != tt.wantKnown {
			t.Errorf("ExpectedExtensionForFormat(%q) = (%q, %v), want (%q, %v)", tt.format, ext, known, tt.wantExt, tt.wantKnown)
		}
	}
}

func TestParseAudioBitrate(t *testing.T) {
	valid := []struct {
		input string
		want  string
	}{
		{"128k", "128k"},
		{"192k", "192k"},
		{"320k", "320k"},
		{"128kbps", "128k"},
		{"192000", "192000"},
		{"1M", "1m"},
	}

	for _, tt := range valid {
		got, err := ParseAudioBitrate(tt.input)
		if err != nil {
			t.Errorf("ParseAudioBitrate(%q) unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("ParseAudioBitrate(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}

	invalid := []string{"", "0", "-128k", "abc", "0k"}
	for _, in := range invalid {
		if _, err := ParseAudioBitrate(in); err == nil {
			t.Errorf("ParseAudioBitrate(%q) expected error, got nil", in)
		}
	}
}

func TestPlanAudio(t *testing.T) {
	ptr := func(v int) *int { return &v }
	ptr64 := func(v int64) *int64 { return &v }

	t.Run("selects audio without video stream and resolves best", func(t *testing.T) {
		media := &Media{
			ID:    "video1",
			Title: "Test Video",
			Formats: []Format{
				{
					ID:         "18",
					Protocol:   ProtocolHTTP,
					Container:  "mp4",
					VideoCodec: "h264",
					AudioCodec: "aac",
					Height:     ptr(360),
					Bitrate:    ptr64(500000),
					Resource:   Resource{URL: "https://example.com/muxed.mp4"},
				},
				{
					ID:         "140",
					Protocol:   ProtocolHTTP,
					Container:  "mp4",
					VideoCodec: "none",
					AudioCodec: "aac",
					Bitrate:    ptr64(128000),
					Resource:   Resource{URL: "https://example.com/audio140.mp4"},
				},
				{
					ID:         "251",
					Protocol:   ProtocolHTTP,
					Container:  "webm",
					VideoCodec: "none",
					AudioCodec: "opus",
					Bitrate:    ptr64(160000),
					Resource:   Resource{URL: "https://example.com/audio251.webm"},
				},
			},
		}

		plan, err := PlanAudio(media, AudioSelection{AudioFormat: "best"})
		if err != nil {
			t.Fatalf("PlanAudio failed: %v", err)
		}

		if plan.Stream.ID != "251" {
			t.Errorf("expected format 251 (Opus higher bitrate), got %s", plan.Stream.ID)
		}
		if plan.OutputSpec.Extension != ".opus" {
			t.Errorf("expected OutputSpec.Extension .opus, got %s", plan.OutputSpec.Extension)
		}
		if !plan.OutputSpec.Copy {
			t.Errorf("expected OutputSpec.Copy true, got %v", plan.OutputSpec.Copy)
		}
	})

	t.Run("rejects when only muxed sources are available", func(t *testing.T) {
		media := &Media{
			ID:    "video2",
			Title: "Muxed Only",
			Formats: []Format{
				{
					ID:         "18",
					Protocol:   ProtocolHTTP,
					Container:  "mp4",
					VideoCodec: "h264",
					AudioCodec: "aac",
					Height:     ptr(360),
					Bitrate:    ptr64(500000),
					Resource:   Resource{URL: "https://example.com/muxed.mp4"},
				},
			},
		}

		_, err := PlanAudio(media, AudioSelection{})
		if !errors.Is(err, ErrMuxedOnlySource) {
			t.Fatalf("expected ErrMuxedOnlySource, got %v", err)
		}
	})

	t.Run("duplicate format IDs across distinct audio tracks", func(t *testing.T) {
		media := &Media{
			ID:    "video3",
			Title: "Multi-Language Tracks",
			Formats: []Format{
				{
					ID:             "140",
					Protocol:       ProtocolHTTP,
					Container:      "mp4",
					VideoCodec:     "none",
					AudioCodec:     "aac",
					Bitrate:        ptr64(128000),
					AudioTrackID:   "es.1",
					AudioTrackName: "Spanish [dubbed]",
					Language:       "es",
					Resource:       Resource{URL: "https://example.com/audio-es.mp4"},
				},
				{
					ID:              "140",
					Protocol:        ProtocolHTTP,
					Container:       "mp4",
					VideoCodec:      "none",
					AudioCodec:      "aac",
					Bitrate:         ptr64(128000),
					AudioTrackID:    "en.4",
					AudioTrackName:  "English (original)",
					AudioIsOriginal: true,
					Language:        "en",
					Resource:        Resource{URL: "https://example.com/audio-en.mp4"},
				},
			},
		}

		planAuto, err := PlanAudio(media, AudioSelection{AudioFormat: "mp3"})
		if err != nil {
			t.Fatalf("PlanAudio auto failed: %v", err)
		}
		if planAuto.Stream.AudioTrackID != "en.4" {
			t.Errorf("expected original English track en.4, got %+v", planAuto.Stream)
		}
		if planAuto.OutputSpec.Extension != ".mp3" {
			t.Errorf("expected .mp3 extension, got %s", planAuto.OutputSpec.Extension)
		}
	})
}
