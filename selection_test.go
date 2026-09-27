package goyt

import (
	"errors"
	"net/http"
	"testing"
)

func selectionFixture(
	id string,
	height int,
	videoCodec string,
	audioCodec string,
) Format {
	f := Format{
		ID:         id,
		Protocol:   ProtocolHTTP,
		Container:  "mp4",
		VideoCodec: videoCodec,
		AudioCodec: audioCodec,
		Language:   "en",
		Resource: Resource{
			URL: "https://fixture.test/" + id,
		},
	}

	if height > 0 {
		f.Height = &height
	}

	return f
}

func TestPlanMaximumHeight(t *testing.T) {
	media := &Media{
		ID: "demo",
		Formats: []Format{
			selectionFixture("720", 720, "h264", "aac"),
			selectionFixture("1080", 1080, "h264", "aac"),
			selectionFixture("2160", 2160, "h264", "aac"),
		},
	}

	plan, err := Plan(media, Selection{MaxHeight: 1080})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Streams[0].ID != "1080" {
		t.Fatalf("selected %s, want 1080", plan.Streams[0].ID)
	}

	if !plan.DirectDownloadable() {
		t.Fatal("combined HTTP MP4 should be directly downloadable")
	}
}

func TestPlanSeparateStreams(t *testing.T) {
	media := &Media{
		ID: "demo",
		Formats: []Format{
			selectionFixture("combined", 720, "h264", "aac"),
			selectionFixture("video", 1080, "avc1.640028", "none"),
			selectionFixture("audio", 0, "none", "mp4a.40.2"),
		},
	}

	plan, err := Plan(media, Selection{
		MaxHeight:     1080,
		AllowSeparate: true,
		VideoCodec:    "h264",
		AudioCodec:    "aac",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Streams) != 2 ||
		plan.Streams[0].ID != "video" ||
		plan.Streams[1].ID != "audio" {
		t.Fatalf("unexpected selection: %+v", plan.Streams)
	}

	if !plan.NeedsMerge || plan.OutputContainer != "mp4" {
		t.Fatal("separate H.264/AAC streams should require an MP4 merge")
	}

	if plan.DirectDownloadable() {
		t.Fatal("Phase 2 cannot execute a merge plan")
	}
}

func TestPlanCombinedFallback(t *testing.T) {
	media := &Media{
		Formats: []Format{
			selectionFixture("combined", 720, "h264", "aac"),
			selectionFixture("video", 1080, "h264", "none"),
			selectionFixture("audio", 0, "none", "aac"),
		},
	}

	plan, err := Plan(media, Selection{})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Streams[0].ID != "combined" || plan.NeedsMerge {
		t.Fatal("separate streams must be explicitly enabled")
	}
}

func TestPlanLanguage(t *testing.T) {
	english := selectionFixture("english", 720, "h264", "aac")
	hindi := selectionFixture("hindi", 1080, "h264", "aac")
	hindi.Language = "hi"

	media := &Media{Formats: []Format{hindi, english}}

	plan, err := Plan(media, Selection{AudioLanguage: "EN"})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Streams[0].ID != "english" {
		t.Fatal("language requirement was ignored")
	}

	_, err = Plan(media, Selection{AudioLanguage: "fr"})
	if !errors.Is(err, ErrNoMatchingFormats) {
		t.Fatalf("got %v, want ErrNoMatchingFormats", err)
	}
}

func TestPlanUnknownHeight(t *testing.T) {
	media := &Media{
		Formats: []Format{
			selectionFixture("unknown", 0, "h264", "aac"),
		},
	}

	_, err := Plan(media, Selection{MaxHeight: 1080})
	if !errors.Is(err, ErrNoMatchingFormats) {
		t.Fatal("unknown height must not satisfy a strict maximum")
	}

	if _, err := Plan(media, Selection{}); err != nil {
		t.Fatalf("unknown height is allowed without a height limit: %v", err)
	}
}

func TestPlanContainerCompatibility(t *testing.T) {
	format := selectionFixture("webm", 1080, "vp09.00.40.08", "opus")
	format.Container = "webm"

	media := &Media{Formats: []Format{format}}

	_, err := Plan(media, Selection{Container: "mp4"})
	if !errors.Is(err, ErrNoMatchingFormats) {
		t.Fatal("unsupported MP4 codec combination should be rejected")
	}

	plan, err := Plan(media, Selection{Container: "mkv"})
	if err != nil {
		t.Fatal(err)
	}

	if !plan.NeedsRemux || plan.NeedsMerge {
		t.Fatal("combined WebM to MKV should require remuxing")
	}

	if plan.DirectDownloadable() {
		t.Fatal("remuxing requires a later processing phase")
	}
}

func TestPlanPrefersCombinedAtEqualHeight(t *testing.T) {
	media := &Media{
		Formats: []Format{
			selectionFixture("video", 1080, "h264", "none"),
			selectionFixture("audio", 0, "none", "aac"),
			selectionFixture("combined", 1080, "h264", "aac"),
		},
	}

	plan, err := Plan(media, Selection{AllowSeparate: true})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Streams[0].ID != "combined" {
		t.Fatal("equal-resolution combined format should win")
	}
}

func TestPlanBitrateAndStableTies(t *testing.T) {
	low := selectionFixture("low", 1080, "h264", "aac")
	high := selectionFixture("high", 1080, "h264", "aac")
	tied := selectionFixture("tied", 1080, "h264", "aac")

	lowRate := int64(1_000_000)
	highRate := int64(2_000_000)

	low.Bitrate = &lowRate
	high.Bitrate = &highRate
	tied.Bitrate = &highRate

	plan, err := Plan(&Media{
		Formats: []Format{low, high, tied},
	}, Selection{})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Streams[0].ID != "high" {
		t.Fatal("expected higher bitrate and stable ordering on ties")
	}
}

func TestPlanCopiesMetadata(t *testing.T) {
	format := selectionFixture("combined", 720, "h264", "aac")
	format.Resource.Headers = http.Header{
		"X-Test": []string{"original"},
	}

	media := &Media{Formats: []Format{format}}

	plan, err := Plan(media, Selection{})
	if err != nil {
		t.Fatal(err)
	}

	*plan.Streams[0].Height = 999
	plan.Streams[0].Resource.Headers.Set("X-Test", "changed")

	if *media.Formats[0].Height != 720 ||
		media.Formats[0].Resource.Headers.Get("X-Test") != "original" {
		t.Fatal("modifying the plan changed the extraction result")
	}
}

func TestPlanAllSupportedCodecContainerCombinations(t *testing.T) {
	tests := []struct {
		name          string
		videoFormat   Format
		audioFormat   Format
		selection     Selection
		wantContainer string
		wantVideoID   string
		wantAudioID   string
	}{
		{
			name:          "H264/AAC MP4",
			videoFormat:   selectionFixture("v-h264", 1080, "avc1.640028", "none"),
			audioFormat:   selectionFixture("a-aac", 0, "none", "mp4a.40.2"),
			selection:     Selection{VideoCodec: "h264", Container: "mp4", AllowSeparate: true},
			wantContainer: "mp4",
			wantVideoID:   "v-h264",
			wantAudioID:   "a-aac",
		},
		{
			name:          "AV1/AAC MP4",
			videoFormat:   selectionFixture("v-av1", 1080, "av01.0.08M.08", "none"),
			audioFormat:   selectionFixture("a-aac", 0, "none", "mp4a.40.2"),
			selection:     Selection{VideoCodec: "av1", Container: "mp4", AllowSeparate: true},
			wantContainer: "mp4",
			wantVideoID:   "v-av1",
			wantAudioID:   "a-aac",
		},
		{
			name:          "VP9/Opus WebM",
			videoFormat:   selectionFixture("v-vp9", 1080, "vp09.00.41.08", "none"),
			audioFormat:   selectionFixture("a-opus", 0, "none", "opus"),
			selection:     Selection{VideoCodec: "vp9", Container: "webm", AllowSeparate: true},
			wantContainer: "webm",
			wantVideoID:   "v-vp9",
			wantAudioID:   "a-opus",
		},
		{
			name:          "AV1/Opus WebM",
			videoFormat:   selectionFixture("v-av1", 1080, "av01.0.08M.08", "none"),
			audioFormat:   selectionFixture("a-opus", 0, "none", "opus"),
			selection:     Selection{VideoCodec: "av1", Container: "webm", AllowSeparate: true},
			wantContainer: "webm",
			wantVideoID:   "v-av1",
			wantAudioID:   "a-opus",
		},
		{
			name:          "H264/AAC MKV",
			videoFormat:   selectionFixture("v-h264", 1080, "h264", "none"),
			audioFormat:   selectionFixture("a-aac", 0, "none", "aac"),
			selection:     Selection{VideoCodec: "h264", Container: "mkv", AllowSeparate: true},
			wantContainer: "mkv",
			wantVideoID:   "v-h264",
			wantAudioID:   "a-aac",
		},
		{
			name:          "H264/Opus MKV",
			videoFormat:   selectionFixture("v-h264", 1080, "h264", "none"),
			audioFormat:   selectionFixture("a-opus", 0, "none", "opus"),
			selection:     Selection{VideoCodec: "h264", Container: "mkv", AllowSeparate: true},
			wantContainer: "mkv",
			wantVideoID:   "v-h264",
			wantAudioID:   "a-opus",
		},
		{
			name:          "VP9/AAC MKV",
			videoFormat:   selectionFixture("v-vp9", 1080, "vp9", "none"),
			audioFormat:   selectionFixture("a-aac", 0, "none", "aac"),
			selection:     Selection{VideoCodec: "vp9", Container: "mkv", AllowSeparate: true},
			wantContainer: "mkv",
			wantVideoID:   "v-vp9",
			wantAudioID:   "a-aac",
		},
		{
			name:          "VP9/Opus MKV",
			videoFormat:   selectionFixture("v-vp9", 1080, "vp9", "none"),
			audioFormat:   selectionFixture("a-opus", 0, "none", "opus"),
			selection:     Selection{VideoCodec: "vp9", Container: "mkv", AllowSeparate: true},
			wantContainer: "mkv",
			wantVideoID:   "v-vp9",
			wantAudioID:   "a-opus",
		},
		{
			name:          "AV1/AAC MKV",
			videoFormat:   selectionFixture("v-av1", 1080, "av1", "none"),
			audioFormat:   selectionFixture("a-aac", 0, "none", "aac"),
			selection:     Selection{VideoCodec: "av1", Container: "mkv", AllowSeparate: true},
			wantContainer: "mkv",
			wantVideoID:   "v-av1",
			wantAudioID:   "a-aac",
		},
		{
			name:          "AV1/Opus MKV",
			videoFormat:   selectionFixture("v-av1", 1080, "av1", "none"),
			audioFormat:   selectionFixture("a-opus", 0, "none", "opus"),
			selection:     Selection{VideoCodec: "av1", Container: "mkv", AllowSeparate: true},
			wantContainer: "mkv",
			wantVideoID:   "v-av1",
			wantAudioID:   "a-opus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			media := &Media{
				ID:      "test-media",
				Formats: []Format{tt.videoFormat, tt.audioFormat},
			}

			plan, err := Plan(media, tt.selection)
			if err != nil {
				t.Fatalf("Plan failed: %v", err)
			}

			if plan.OutputContainer != tt.wantContainer {
				t.Fatalf("OutputContainer = %q, want %q", plan.OutputContainer, tt.wantContainer)
			}
			if len(plan.Streams) != 2 || plan.Streams[0].ID != tt.wantVideoID || plan.Streams[1].ID != tt.wantAudioID {
				t.Fatalf("unexpected streams: %+v", plan.Streams)
			}
		})
	}
}

func TestPlanStrictMissingCodecNoSilentFallback(t *testing.T) {
	h264Video := selectionFixture("v-h264", 1080, "h264", "none")
	aacAudio := selectionFixture("a-aac", 0, "none", "aac")
	media := &Media{
		ID:      "only-h264",
		Formats: []Format{h264Video, aacAudio},
	}

	// Requesting VP9 when only H.264 is available must fail with ErrNoMatchingFormats (no silent fallback to H.264)
	_, err := Plan(media, Selection{VideoCodec: "vp9", AllowSeparate: true})
	if !errors.Is(err, ErrNoMatchingFormats) {
		t.Fatalf("expected ErrNoMatchingFormats for missing VP9, got: %v", err)
	}

	// Requesting AV1 when only H.264 is available must fail
	_, err = Plan(media, Selection{VideoCodec: "av1", AllowSeparate: true})
	if !errors.Is(err, ErrNoMatchingFormats) {
		t.Fatalf("expected ErrNoMatchingFormats for missing AV1, got: %v", err)
	}

	vp9Media := &Media{
		ID: "only-vp9",
		Formats: []Format{
			selectionFixture("v-vp9", 1080, "vp9", "none"),
			selectionFixture("a-opus", 0, "none", "opus"),
		},
	}

	// Requesting H.264 when only VP9 is available must fail (no silent fallback)
	_, err = Plan(vp9Media, Selection{VideoCodec: "h264", AllowSeparate: true})
	if !errors.Is(err, ErrNoMatchingFormats) {
		t.Fatalf("expected ErrNoMatchingFormats for missing H.264, got: %v", err)
	}
}

func TestPlanMKVAudioRanking(t *testing.T) {
	v := selectionFixture("video", 1080, "h264", "none")

	rate128 := int64(128000)
	rate160 := int64(160000)
	rate256 := int64(256000)

	t.Run("Opus preferred over AAC regardless of bitrate", func(t *testing.T) {
		// Even if AAC has higher bitrate (256k) than Opus (128k), Opus is deterministic preference for MKV
		aac := selectionFixture("a-aac", 0, "none", "aac")
		aac.Bitrate = &rate256

		opus := selectionFixture("a-opus", 0, "none", "opus")
		opus.Bitrate = &rate128

		media := &Media{Formats: []Format{v, aac, opus}}
		plan, err := Plan(media, Selection{Container: "mkv", AllowSeparate: true})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Streams[1].ID != "a-opus" {
			t.Fatalf("expected Opus audio to be preferred over AAC for MKV, got: %s", plan.Streams[1].ID)
		}
	})

	t.Run("Higher bitrate wins within same codec", func(t *testing.T) {
		opus128 := selectionFixture("opus-128", 0, "none", "opus")
		opus128.Bitrate = &rate128

		opus160 := selectionFixture("opus-160", 0, "none", "opus")
		opus160.Bitrate = &rate160

		media := &Media{Formats: []Format{v, opus128, opus160}}
		plan, err := Plan(media, Selection{Container: "mkv", AllowSeparate: true})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Streams[1].ID != "opus-160" {
			t.Fatalf("expected higher bitrate Opus to win, got: %s", plan.Streams[1].ID)
		}
	})

	t.Run("Original audio wins over non-original regardless of codec rank", func(t *testing.T) {
		aacOrig := selectionFixture("aac-orig", 0, "none", "aac")
		aacOrig.AudioIsOriginal = true
		aacOrig.Bitrate = &rate128

		opusNonOrig := selectionFixture("opus-non-orig", 0, "none", "opus")
		opusNonOrig.AudioIsOriginal = false
		opusNonOrig.Bitrate = &rate160

		media := &Media{Formats: []Format{v, opusNonOrig, aacOrig}}
		plan, err := Plan(media, Selection{Container: "mkv", AllowSeparate: true})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Streams[1].ID != "aac-orig" {
			t.Fatalf("expected original audio track to win, got: %s", plan.Streams[1].ID)
		}
	})
}

func TestPlanValidation(t *testing.T) {
	if _, err := Plan(nil, Selection{}); err == nil {
		t.Fatal("nil media should be rejected")
	}

	for _, selection := range []Selection{
		{MaxHeight: -1},
		{VideoCodec: "unsupported"},
		{AudioCodec: "unsupported"},
		{Container: "unsupported"},
		{VideoCodec: "vp9", Container: "mp4"},
		{VideoCodec: "h264", Container: "webm"},
	} {
		if _, err := Plan(&Media{}, selection); err == nil {
			t.Fatalf("invalid selection accepted: %+v", selection)
		}
	}

	_, err := Plan(&Media{}, Selection{})
	if !errors.Is(err, ErrNoMatchingFormats) {
		t.Fatal("empty formats should return ErrNoMatchingFormats")
	}
}

func TestCodecNormalization(t *testing.T) {
	t.Run("video codecs", func(t *testing.T) {
		valid := map[string]string{
			"h264":          "h264",
			"H264":          "h264",
			"avc1":          "h264",
			"avc1.640028":   "h264",
			"avc1.4d401f":   "h264",
			"avc3":          "h264",
			"avc3.42001e":   "h264",
			"vp9":           "vp9",
			"VP9":           "vp9",
			"vp09":          "vp9",
			"vp09.00.41.08": "vp9",
			"vp09.00.51.08": "vp9",
			"av1":           "av1",
			"AV1":           "av1",
			"av01":          "av1",
			"av01.0.05m.08": "av1",
			"av01.0.08m.08": "av1",
		}
		for in, want := range valid {
			got, err := NormalizeVideoCodec(in)
			if err != nil {
				t.Errorf("NormalizeVideoCodec(%q) unexpected error: %v", in, err)
			}
			if got != want {
				t.Errorf("NormalizeVideoCodec(%q) = %q, want %q", in, got, want)
			}
		}

		invalid := []string{"hevc", "h265", "vp8", "mp4v", "unknown"}
		for _, in := range invalid {
			if _, err := NormalizeVideoCodec(in); err == nil {
				t.Errorf("NormalizeVideoCodec(%q) expected error, got nil", in)
			}
		}
	})

	t.Run("audio codecs", func(t *testing.T) {
		valid := map[string]string{
			"aac":        "aac",
			"AAC":        "aac",
			"mp4a.40.2":  "aac",
			"mp4a.40.5":  "aac",
			"mp4a.40.29": "aac",
			"mp4a.40.1":  "aac",
			"mp4a.40.3":  "aac",
			"mp4a.40.4":  "aac",
			"mp4a.40.6":  "aac",
			"mp4a.66":    "aac",
			"mp4a.67":    "aac",
			"mp4a.68":    "aac",
			"opus":       "opus",
			"OPUS":       "opus",
		}
		for in, want := range valid {
			got, err := NormalizeAudioCodec(in)
			if err != nil {
				t.Errorf("NormalizeAudioCodec(%q) unexpected error: %v", in, err)
			}
			if got != want {
				t.Errorf("NormalizeAudioCodec(%q) = %q, want %q", in, got, want)
			}
			if normalizeCodec(in) != want {
				t.Errorf("normalizeCodec(%q) = %q, want %q", in, normalizeCodec(in), want)
			}
		}

		// Non-allowlisted mp4a object types, malformed identifiers, and unsupported codecs must not normalize to aac
		invalid := []string{
			"mp4a.40.34",
			"mp4a.40.999",
			"mp4a.40.",
			"mp4a.40.xyz",
			"mp4a.6b",
			"mp4a.a5",
			"mp4a.ec",
			"mp4a.",
			"mp4a",
			"mp3",
			"flac",
			"vorbis",
			"alac",
			"unknown",
		}
		for _, in := range invalid {
			if _, err := NormalizeAudioCodec(in); err == nil {
				t.Errorf("NormalizeAudioCodec(%q) expected error, got nil", in)
			}
			if normalizeCodec(in) == "aac" {
				t.Errorf("normalizeCodec(%q) unexpectedly returned %q", in, "aac")
			}
		}
	})
}
