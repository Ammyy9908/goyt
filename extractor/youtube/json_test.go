package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ammyy9908/goyt"
)

func TestBuildInspectResponse_SingleClientSuccess(t *testing.T) {
	duration := 262 * time.Second
	width := 1920
	height := 1080
	bitrate := int64(4500000)

	report := &Report{
		Media: &goyt.Media{
			ID:        "VIDEO_ID_123",
			Title:     "Sample Video Title",
			Duration:  &duration,
			SourceURL: "https://www.youtube.com/watch?v=VIDEO_ID_123",
		},
		PlaybackStatus:  "OK",
		PlaybackReason:  "",
		HasHLSManifest:  true,
		HasDASHManifest: false,
		HasSABREndpoint: true,
		Formats: []DiscoveredFormat{
			{
				ID:                 137,
				MIMEType:           "video/mp4",
				Codecs:             "avc1.640028",
				Quality:            "1080p",
				Width:              width,
				Height:             height,
				Bitrate:            bitrate,
				HasDirectURL:       false,
				SignatureChallenge: true,
				NChallenge:         true,
				DRMReported:        false,
			},
			{
				ID:                 140,
				MIMEType:           "audio/mp4",
				Codecs:             "mp4a.40.2",
				Quality:            "tiny",
				Bitrate:            128000,
				HasDirectURL:       true,
				SignatureChallenge: false,
				NChallenge:         false,
				DRMReported:        false,
			},
		},
		AudioTracks: []DiscoveredAudioTrack{
			{
				ID:             "en.4",
				DisplayName:    "English (original)",
				AudioIsDefault: true,
				IsOriginal:     true,
			},
		},
		HLSRenditions: []DiscoveredHLSRendition{
			{
				GroupID:    "audio",
				Name:       "English (original)",
				Language:   "en",
				Default:    true,
				AutoSelect: true,
				IsOriginal: true,
			},
		},
		Limitations: []string{
			"Discovered URLs have not been verified for playback or downloading.",
		},
	}

	clientResult := BuildClientInspectResult("visionos", report, nil)
	resp := BuildInspectResponse([]ClientInspectResult{clientResult})

	if resp.SchemaVersion != 1 {
		t.Fatalf("expected schema_version 1, got %d", resp.SchemaVersion)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}

	res := resp.Results[0]
	if res.Client != "visionos" || res.Status != "ok" {
		t.Fatalf("unexpected client/status: %+v", res)
	}
	if res.Error != nil {
		t.Fatalf("expected nil error, got %+v", res.Error)
	}
	if res.Media == nil || res.Media.ID != "VIDEO_ID_123" || res.Media.Title != "Sample Video Title" {
		t.Fatalf("unexpected media: %+v", res.Media)
	}
	if res.Media.DurationSeconds == nil || *res.Media.DurationSeconds != 262.0 {
		t.Fatalf("unexpected duration_seconds: %v", res.Media.DurationSeconds)
	}
	if res.Playback == nil || res.Playback.Status != "OK" || res.Playback.Reason != nil {
		t.Fatalf("unexpected playback: %+v", res.Playback)
	}
	if res.Streaming == nil || !res.Streaming.HLS || res.Streaming.DASH || !res.Streaming.SABR {
		t.Fatalf("unexpected streaming: %+v", res.Streaming)
	}
	if len(res.AvailableVideoHeights) != 1 || res.AvailableVideoHeights[0] != 1080 {
		t.Fatalf("unexpected available_video_heights: %v", res.AvailableVideoHeights)
	}
	if len(res.Formats) != 2 {
		t.Fatalf("expected 2 formats, got %d", len(res.Formats))
	}
	if res.Formats[0].ID != 137 || res.Formats[0].MIMEType != "video/mp4" ||
		res.Formats[0].Codecs == nil || *res.Formats[0].Codecs != "avc1.640028" ||
		res.Formats[0].Quality == nil || *res.Formats[0].Quality != "1080p" ||
		res.Formats[0].Width == nil || *res.Formats[0].Width != 1920 ||
		res.Formats[0].Height == nil || *res.Formats[0].Height != 1080 ||
		res.Formats[0].Bitrate == nil || *res.Formats[0].Bitrate != 4500000 ||
		res.Formats[0].HasDirectURL || !res.Formats[0].SignatureChallenge ||
		!res.Formats[0].NChallenge || res.Formats[0].DRMReported {
		t.Fatalf("unexpected format 0: %+v", res.Formats[0])
	}
	if len(res.AudioTracks) != 1 || res.AudioTracks[0].ID != "en.4" ||
		res.AudioTracks[0].Name != "English (original)" || res.AudioTracks[0].Language != nil ||
		!res.AudioTracks[0].Default || !res.AudioTracks[0].OriginalHint {
		t.Fatalf("unexpected audio track: %+v", res.AudioTracks[0])
	}
	if len(res.HLSAudioRenditions) != 1 || res.HLSAudioRenditions[0].GroupID != "audio" ||
		res.HLSAudioRenditions[0].Name != "English (original)" ||
		res.HLSAudioRenditions[0].Language == nil || *res.HLSAudioRenditions[0].Language != "en" ||
		!res.HLSAudioRenditions[0].Default || !res.HLSAudioRenditions[0].AutoSelect ||
		!res.HLSAudioRenditions[0].OriginalHint {
		t.Fatalf("unexpected HLS rendition: %+v", res.HLSAudioRenditions[0])
	}

	// Test JSON serialization
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if parsed["schema_version"] != float64(1) {
		t.Fatalf("schema_version = %v; want 1", parsed["schema_version"])
	}
}

func TestBuildInspectResponse_MultipleClientSuccess(t *testing.T) {
	webReport := &Report{
		Media:          &goyt.Media{ID: "ABC12345678", Title: "Title 1"},
		PlaybackStatus: "OK",
	}
	visionReport := &Report{
		Media:          &goyt.Media{ID: "ABC12345678", Title: "Title 1"},
		PlaybackStatus: "OK",
		HasHLSManifest: true,
	}

	res1 := BuildClientInspectResult("web", webReport, nil)
	res2 := BuildClientInspectResult("visionos", visionReport, nil)
	resp := BuildInspectResponse([]ClientInspectResult{res1, res2})

	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if resp.Results[0].Client != "web" || resp.Results[1].Client != "visionos" {
		t.Fatalf("unexpected client order: %v, %v", resp.Results[0].Client, resp.Results[1].Client)
	}
}

func TestBuildInspectResponse_PartialFailure(t *testing.T) {
	webReport := &Report{
		Media:          &goyt.Media{ID: "ABC12345678", Title: "Title 1"},
		PlaybackStatus: "OK",
	}
	visionErr := errors.New("youtube: visionos player API returned HTTP 500")

	res1 := BuildClientInspectResult("web", webReport, nil)
	res2 := BuildClientInspectResult("visionos", nil, visionErr)
	resp := BuildInspectResponse([]ClientInspectResult{res1, res2})

	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}

	// web client succeeded
	if resp.Results[0].Client != "web" || resp.Results[0].Status != "ok" || resp.Results[0].Error != nil {
		t.Fatalf("unexpected web result: %+v", resp.Results[0])
	}

	// visionos client failed
	if resp.Results[1].Client != "visionos" || resp.Results[1].Status != "error" {
		t.Fatalf("unexpected visionos result: %+v", resp.Results[1])
	}
	if resp.Results[1].Error == nil || resp.Results[1].Error.Code != "extraction_failed" {
		t.Fatalf("unexpected error: %+v", resp.Results[1].Error)
	}
	if resp.Results[1].Media != nil || resp.Results[1].Playback != nil || resp.Results[1].Streaming != nil {
		t.Fatalf("expected nil media/playback/streaming on error, got %+v", resp.Results[1])
	}
	if len(resp.Results[1].AvailableVideoHeights) != 0 || len(resp.Results[1].Formats) != 0 ||
		len(resp.Results[1].AudioTracks) != 0 || len(resp.Results[1].HLSAudioRenditions) != 0 ||
		len(resp.Results[1].Limitations) != 0 {
		t.Fatalf("expected empty slices on error result, got %+v", resp.Results[1])
	}

	// Verify JSON marshaling produces [] and not null for collections
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	jsonStr := string(data)
	if strings.Contains(jsonStr, `"available_video_heights":null`) ||
		strings.Contains(jsonStr, `"formats":null`) ||
		strings.Contains(jsonStr, `"audio_tracks":null`) ||
		strings.Contains(jsonStr, `"hls_audio_renditions":null`) ||
		strings.Contains(jsonStr, `"limitations":null`) {
		t.Fatalf("found null collections in JSON: %s", jsonStr)
	}
}

func TestBuildInspectResponse_TotalFailure(t *testing.T) {
	err := errors.New("youtube: network unreachable")
	res := BuildClientInspectResult("web", nil, err)
	resp := BuildInspectResponse([]ClientInspectResult{res})

	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	if resp.Results[0].Status != "error" || resp.Results[0].Error == nil {
		t.Fatalf("expected status error, got %+v", resp.Results[0])
	}
	if resp.Results[0].Error.Code != "extraction_failed" {
		t.Fatalf("expected code extraction_failed, got %s", resp.Results[0].Error.Code)
	}
}

func TestBuildInspectResponse_RestrictedPlayback(t *testing.T) {
	report := &Report{
		Media:          &goyt.Media{ID: "RESTRICTED12", Title: "Age-Restricted Video"},
		PlaybackStatus: "LOGIN_REQUIRED",
		PlaybackReason: "This video may be inappropriate for some users.",
	}

	res := BuildClientInspectResult("web", report, nil)

	if res.Status != "ok" {
		t.Fatalf("restricted playback should have status 'ok', got %q", res.Status)
	}
	if res.Error != nil {
		t.Fatalf("restricted playback should have nil error, got %+v", res.Error)
	}
	if res.Playback == nil || res.Playback.Status != "LOGIN_REQUIRED" {
		t.Fatalf("expected LOGIN_REQUIRED, got %+v", res.Playback)
	}
	if res.Playback.Reason == nil || *res.Playback.Reason != "This video may be inappropriate for some users." {
		t.Fatalf("expected reason, got %v", res.Playback.Reason)
	}
}

func TestBuildInspectResponse_UnknownDurationAndDimensions(t *testing.T) {
	report := &Report{
		Media: &goyt.Media{
			ID:       "UNKNOWN_DUR",
			Title:    "No Duration Video",
			Duration: nil,
		},
		PlaybackStatus: "OK",
		Formats: []DiscoveredFormat{
			{
				ID:           18,
				MIMEType:     "video/mp4",
				Codecs:       "",
				Quality:      "",
				Width:        0,
				Height:       0,
				Bitrate:      0,
				HasDirectURL: true,
			},
		},
	}

	res := BuildClientInspectResult("web", report, nil)

	if res.Media.DurationSeconds != nil {
		t.Fatalf("expected nil duration_seconds, got %v", *res.Media.DurationSeconds)
	}
	if res.Formats[0].Codecs != nil {
		t.Fatalf("expected nil codecs, got %v", *res.Formats[0].Codecs)
	}
	if res.Formats[0].Quality != nil {
		t.Fatalf("expected nil quality, got %v", *res.Formats[0].Quality)
	}
	if res.Formats[0].Width != nil {
		t.Fatalf("expected nil width, got %v", *res.Formats[0].Width)
	}
	if res.Formats[0].Height != nil {
		t.Fatalf("expected nil height, got %v", *res.Formats[0].Height)
	}
	if res.Formats[0].Bitrate != nil {
		t.Fatalf("expected nil bitrate, got %v", *res.Formats[0].Bitrate)
	}

	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	jsonStr := string(data)
	if !strings.Contains(jsonStr, `"duration_seconds":null`) {
		t.Fatalf("duration_seconds should be null in JSON: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"width":null`) {
		t.Fatalf("width should be null in JSON: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"height":null`) {
		t.Fatalf("height should be null in JSON: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"bitrate":null`) {
		t.Fatalf("bitrate should be null in JSON: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"codecs":null`) {
		t.Fatalf("codecs should be null in JSON: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"quality":null`) {
		t.Fatalf("quality should be null in JSON: %s", jsonStr)
	}
}

func TestBuildInspectResponse_EmptyArrays(t *testing.T) {
	report := &Report{
		Media:          &goyt.Media{ID: "EMPTY_ARRS", Title: "Empty"},
		PlaybackStatus: "OK",
		Formats:        nil,
		AudioTracks:    nil,
		HLSRenditions:  nil,
		Limitations:    nil,
	}

	res := BuildClientInspectResult("web", report, nil)

	if res.AvailableVideoHeights == nil || len(res.AvailableVideoHeights) != 0 {
		t.Fatalf("expected non-nil empty heights slice")
	}
	if res.Formats == nil || len(res.Formats) != 0 {
		t.Fatalf("expected non-nil empty formats slice")
	}
	if res.AudioTracks == nil || len(res.AudioTracks) != 0 {
		t.Fatalf("expected non-nil empty audio_tracks slice")
	}
	if res.HLSAudioRenditions == nil || len(res.HLSAudioRenditions) != 0 {
		t.Fatalf("expected non-nil empty hls_audio_renditions slice")
	}
	if res.Limitations == nil || len(res.Limitations) != 0 {
		t.Fatalf("expected non-nil empty limitations slice")
	}

	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw failed: %v", err)
	}

	for _, field := range []string{"available_video_heights", "formats", "audio_tracks", "hls_audio_renditions", "limitations"} {
		if string(raw[field]) != "[]" {
			t.Fatalf("field %s expected to be [], got %s", field, string(raw[field]))
		}
	}
}

func TestBuildInspectResponse_SortedDeduplicatedHeights(t *testing.T) {
	report := &Report{
		Media:          &goyt.Media{ID: "HEIGHTS1234", Title: "Heights Test"},
		PlaybackStatus: "OK",
		Formats: []DiscoveredFormat{
			{ID: 1, Height: 1080},
			{ID: 2, Height: 720},
			{ID: 3, Height: 1080}, // duplicate
			{ID: 4, Height: 480},
			{ID: 5, Height: 0},  // audio / non-positive
			{ID: 6, Height: -5}, // invalid
			{ID: 7, Height: 360},
			{ID: 8, Height: 144},
		},
	}

	res := BuildClientInspectResult("web", report, nil)
	expected := []int{144, 360, 480, 720, 1080}

	if len(res.AvailableVideoHeights) != len(expected) {
		t.Fatalf("expected heights %v, got %v", expected, res.AvailableVideoHeights)
	}
	for i, h := range expected {
		if res.AvailableVideoHeights[i] != h {
			t.Fatalf("at index %d, expected %d, got %d", i, h, res.AvailableVideoHeights[i])
		}
	}
}

func TestBuildInspectResponse_DuplicateFormatIDsAcrossAudioTracks(t *testing.T) {
	report := &Report{
		Media:          &goyt.Media{ID: "DUP_FORMATS", Title: "Duplicate Formats"},
		PlaybackStatus: "OK",
		Formats: []DiscoveredFormat{
			{ID: 140, MIMEType: "audio/mp4", Quality: "tiny", Bitrate: 128000},
			{ID: 140, MIMEType: "audio/mp4", Quality: "tiny", Bitrate: 128000}, // same itag for different audio track
			{ID: 140, MIMEType: "audio/mp4", Quality: "tiny", Bitrate: 128000},
		},
	}

	res := BuildClientInspectResult("web", report, nil)

	if len(res.Formats) != 3 {
		t.Fatalf("expected 3 formats preserved, got %d", len(res.Formats))
	}
	for i, f := range res.Formats {
		if f.ID != 140 {
			t.Fatalf("format %d has id %d, want 140", i, f.ID)
		}
	}
}

func TestBuildInspectResponse_Unicode(t *testing.T) {
	unicodeTitle := "日本語タイトル 🎬 • Emoji & 特殊文字 [テスト]"
	unicodeAudioName := "Español (Latinoamérica) (original) 🎵"

	report := &Report{
		Media: &goyt.Media{
			ID:    "UNICODE1234",
			Title: unicodeTitle,
		},
		PlaybackStatus: "OK",
		AudioTracks: []DiscoveredAudioTrack{
			{
				ID:             "es-419.1",
				DisplayName:    unicodeAudioName,
				AudioIsDefault: true,
				IsOriginal:     true,
			},
		},
	}

	res := BuildClientInspectResult("web", report, nil)

	if res.Media.Title != unicodeTitle {
		t.Fatalf("title corrupted: %q", res.Media.Title)
	}
	if res.AudioTracks[0].Name != unicodeAudioName {
		t.Fatalf("audio track name corrupted: %q", res.AudioTracks[0].Name)
	}

	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal unicode failed: %v", err)
	}

	var parsed ClientInspectResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal unicode failed: %v", err)
	}
	if parsed.Media.Title != unicodeTitle {
		t.Fatalf("round-trip title corrupted: %q", parsed.Media.Title)
	}
	if parsed.AudioTracks[0].Name != unicodeAudioName {
		t.Fatalf("round-trip audio name corrupted: %q", parsed.AudioTracks[0].Name)
	}
}

func TestBuildInspectResponse_SensitiveDataExcluded(t *testing.T) {
	report := &Report{
		Media: &goyt.Media{
			ID:        "SENSITIVE12",
			Title:     "Safe Title",
			SourceURL: "https://www.youtube.com/watch?v=SENSITIVE12",
		},
		PlaybackStatus: "OK",
		Formats: []DiscoveredFormat{
			{
				ID:                 18,
				MIMEType:           "video/mp4",
				HasDirectURL:       true,
				SignatureChallenge: true,
				NChallenge:         true,
			},
		},
	}

	res := BuildClientInspectResult("visionos", report, nil)
	resp := BuildInspectResponse([]ClientInspectResult{res})

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	jsonStr := string(data)

	// Ensure sensitive words and URLs are not present in JSON output
	sensitivePatterns := []string{
		"googlevideo.com",
		"videoplayback",
		"signatureCipher",
		"visitorData",
		"VISITOR_DATA",
		"X-Goog-Visitor-Id",
		"X-YouTube-Client",
		"poToken",
		"sessionIndex",
		"Authorization",
		"Cookie",
		"https://www.youtube.com/watch?v=SENSITIVE12",
	}

	for _, pattern := range sensitivePatterns {
		if strings.Contains(jsonStr, pattern) {
			t.Fatalf("JSON output contains sensitive data %q: %s", pattern, jsonStr)
		}
	}
}

func TestBuildInspectErrorDTO_Codes(t *testing.T) {
	cases := []struct {
		err          error
		expectedCode string
	}{
		{context.Canceled, "context_canceled"},
		{context.DeadlineExceeded, "timeout"},
		{ErrPlayerResponseMissing, "player_response_missing"},
		{errors.New("generic error"), "extraction_failed"},
	}

	for _, tc := range cases {
		res := BuildClientInspectResult("web", nil, tc.err)
		if res.Error == nil {
			t.Fatalf("expected error for %v, got nil", tc.err)
		}
		if res.Error.Code != tc.expectedCode {
			t.Fatalf("err %v: got code %q, want %q", tc.err, res.Error.Code, tc.expectedCode)
		}
	}
}
