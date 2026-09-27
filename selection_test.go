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

func TestPlanValidation(t *testing.T) {
	if _, err := Plan(nil, Selection{}); err == nil {
		t.Fatal("nil media should be rejected")
	}

	for _, selection := range []Selection{
		{MaxHeight: -1},
		{VideoCodec: "unsupported"},
		{AudioCodec: "unsupported"},
		{Container: "unsupported"},
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
