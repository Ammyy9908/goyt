package goyt

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestMatchRefreshedFormat(t *testing.T) {
	h720 := 720
	w1280 := 1280
	h1080 := 1080
	w1920 := 1920

	origVideo := Format{
		ID:              "137",
		Protocol:        "https",
		Container:       "mp4",
		VideoCodec:      "avc1.640028",
		AudioCodec:      "none",
		Height:          &h1080,
		Width:           &w1920,
		AudioTrackID:    "",
		Language:        "",
		AudioIsOriginal: false,
		AudioIsDefault:  false,
		Resource:        Resource{URL: "https://media.test/old_1080.mp4"},
	}

	origAudio := Format{
		ID:              "140",
		Protocol:        "https",
		Container:       "m4a",
		VideoCodec:      "none",
		AudioCodec:      "mp4a.40.2",
		AudioTrackID:    "en-orig",
		Language:        "en",
		AudioIsOriginal: true,
		AudioIsDefault:  true,
		Resource:        Resource{URL: "https://media.test/old_audio.m4a"},
	}

	t.Run("exact match video", func(t *testing.T) {
		refreshedURL := "https://media.test/new_1080.mp4"
		candidates := []Format{
			{
				ID:         "18",
				Protocol:   "https",
				Container:  "mp4",
				VideoCodec: "avc1.42001E",
				AudioCodec: "mp4a.40.2",
				Height:     &h720,
				Width:      &w1280,
				Resource:   Resource{URL: "https://media.test/360.mp4"},
			},
			{
				ID:         "137",
				Protocol:   "https",
				Container:  "mp4",
				VideoCodec: "avc1.640028",
				AudioCodec: "none",
				Height:     &h1080,
				Width:      &w1920,
				Resource:   Resource{URL: refreshedURL},
			},
		}

		matched, err := MatchRefreshedFormat(origVideo, candidates)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched.Resource.URL != refreshedURL {
			t.Fatalf("got URL %q, want %q", matched.Resource.URL, refreshedURL)
		}
	})

	t.Run("exact match audio with track identity", func(t *testing.T) {
		refreshedURL := "https://media.test/new_audio_en.m4a"
		candidates := []Format{
			{
				ID:              "140",
				Protocol:        "https",
				Container:       "m4a",
				VideoCodec:      "none",
				AudioCodec:      "mp4a.40.2",
				AudioTrackID:    "es-dub",
				Language:        "es",
				AudioIsOriginal: false,
				AudioIsDefault:  false,
				Resource:        Resource{URL: "https://media.test/new_audio_es.m4a"},
			},
			{
				ID:              "140",
				Protocol:        "https",
				Container:       "m4a",
				VideoCodec:      "none",
				AudioCodec:      "mp4a.40.2",
				AudioTrackID:    "en-orig",
				Language:        "en",
				AudioIsOriginal: true,
				AudioIsDefault:  true,
				Resource:        Resource{URL: refreshedURL},
			},
		}

		matched, err := MatchRefreshedFormat(origAudio, candidates)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched.Resource.URL != refreshedURL {
			t.Fatalf("got URL %q, want %q", matched.Resource.URL, refreshedURL)
		}
	})

	t.Run("rejects same itag with different audio track", func(t *testing.T) {
		candidates := []Format{
			{
				ID:              "140",
				Protocol:        "https",
				Container:       "m4a",
				VideoCodec:      "none",
				AudioCodec:      "mp4a.40.2",
				AudioTrackID:    "fr-dub",
				Language:        "fr",
				AudioIsOriginal: false,
				AudioIsDefault:  false,
				Resource:        Resource{URL: "https://media.test/new_audio_fr.m4a"},
			},
		}

		_, err := MatchRefreshedFormat(origAudio, candidates)
		if err == nil {
			t.Fatal("expected error for mismatched audio track, got nil")
		}
	})

	t.Run("rejects format missing from candidates", func(t *testing.T) {
		candidates := []Format{
			{
				ID:         "18",
				Protocol:   "https",
				Container:  "mp4",
				VideoCodec: "avc1.42001E",
				AudioCodec: "mp4a.40.2",
				Height:     &h720,
				Width:      &w1280,
				Resource:   Resource{URL: "https://media.test/360.mp4"},
			},
		}

		_, err := MatchRefreshedFormat(origVideo, candidates)
		if err == nil {
			t.Fatal("expected error for missing format, got nil")
		}
	})

	t.Run("rejects ambiguous formats", func(t *testing.T) {
		candidates := []Format{
			origVideo,
			origVideo,
		}

		_, err := MatchRefreshedFormat(origVideo, candidates)
		if err == nil {
			t.Fatal("expected error for ambiguous formats, got nil")
		}
	})

	t.Run("rejects codec mismatch", func(t *testing.T) {
		mismatchedCodec := origVideo
		mismatchedCodec.VideoCodec = "vp09.00.51.08"
		candidates := []Format{mismatchedCodec}

		_, err := MatchRefreshedFormat(origVideo, candidates)
		if err == nil {
			t.Fatal("expected error for codec mismatch, got nil")
		}
	})

	t.Run("rejects dimension mismatch", func(t *testing.T) {
		mismatchedDim := origVideo
		mismatchedDim.Height = &h720
		candidates := []Format{mismatchedDim}

		_, err := MatchRefreshedFormat(origVideo, candidates)
		if err == nil {
			t.Fatal("expected error for dimension mismatch, got nil")
		}
	})
}

func TestMatchRefreshedVariant(t *testing.T) {
	orig := HLSVariant{
		URL:        "https://manifest.test/1080p.m3u8",
		Bandwidth:  4500000,
		Width:      1920,
		Height:     1080,
		Codecs:     "avc1.640028,mp4a.40.2",
		AudioGroup: "audio-en",
	}

	t.Run("matches variant by resolution and codecs", func(t *testing.T) {
		master := &HLSMaster{
			Variants: []HLSVariant{
				{
					URL:        "https://manifest.test/720p.m3u8",
					Bandwidth:  2500000,
					Width:      1280,
					Height:     720,
					Codecs:     "avc1.4d401f,mp4a.40.2",
					AudioGroup: "audio-en",
				},
				{
					URL:        "https://manifest.test/new_1080p.m3u8",
					Bandwidth:  4500000,
					Width:      1920,
					Height:     1080,
					Codecs:     "avc1.640028,mp4a.40.2",
					AudioGroup: "audio-en",
				},
			},
		}

		matched, err := MatchRefreshedVariant(orig, master)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched.URL != "https://manifest.test/new_1080p.m3u8" {
			t.Fatalf("got URL %q, want new 1080p", matched.URL)
		}
	})

	t.Run("matches variant with frame rate", func(t *testing.T) {
		orig60 := HLSVariant{
			URL:        "https://manifest.test/1080p_60fps.m3u8",
			Width:      1920,
			Height:     1080,
			Codecs:     "avc1.640028,mp4a.40.2",
			FrameRate:  "60.000",
			AudioGroup: "audio-en",
		}
		master := &HLSMaster{
			Variants: []HLSVariant{
				{
					URL:        "https://manifest.test/new_1080p_30fps.m3u8",
					Width:      1920,
					Height:     1080,
					Codecs:     "avc1.640028,mp4a.40.2",
					FrameRate:  "30.000",
					AudioGroup: "audio-en",
				},
				{
					URL:        "https://manifest.test/new_1080p_60fps.m3u8",
					Width:      1920,
					Height:     1080,
					Codecs:     "avc1.640028,mp4a.40.2",
					FrameRate:  "60.000",
					AudioGroup: "audio-en",
				},
			},
		}

		matched, err := MatchRefreshedVariant(orig60, master)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched.URL != "https://manifest.test/new_1080p_60fps.m3u8" {
			t.Fatalf("got URL %q, want 60fps variant", matched.URL)
		}
	})

	t.Run("rejects variant with missing or mismatched frame rate", func(t *testing.T) {
		orig60 := HLSVariant{
			URL:        "https://manifest.test/1080p_60fps.m3u8",
			Width:      1920,
			Height:     1080,
			Codecs:     "avc1.640028,mp4a.40.2",
			FrameRate:  "60.000",
			AudioGroup: "audio-en",
		}
		master := &HLSMaster{
			Variants: []HLSVariant{
				{
					URL:        "https://manifest.test/new_1080p_no_fps.m3u8",
					Width:      1920,
					Height:     1080,
					Codecs:     "avc1.640028,mp4a.40.2",
					FrameRate:  "",
					AudioGroup: "audio-en",
				},
			},
		}

		_, err := MatchRefreshedVariant(orig60, master)
		if err == nil {
			t.Fatal("expected error when frame rate is missing in candidate, got nil")
		}
	})

	t.Run("rejects variant with mismatched frame rate", func(t *testing.T) {
		orig60 := HLSVariant{
			URL:        "https://manifest.test/1080p_60fps.m3u8",
			Width:      1920,
			Height:     1080,
			Codecs:     "avc1.640028,mp4a.40.2",
			FrameRate:  "60.000",
			AudioGroup: "audio-en",
		}
		master := &HLSMaster{
			Variants: []HLSVariant{
				{
					URL:        "https://manifest.test/new_1080p_30fps.m3u8",
					Width:      1920,
					Height:     1080,
					Codecs:     "avc1.640028,mp4a.40.2",
					FrameRate:  "30.000",
					AudioGroup: "audio-en",
				},
			},
		}

		_, err := MatchRefreshedVariant(orig60, master)
		if err == nil {
			t.Fatal("expected error when frame rate is mismatched (60fps vs 30fps), got nil")
		}
	})

	t.Run("does not use bandwidth to select between different frame rates", func(t *testing.T) {
		orig60 := HLSVariant{
			URL:        "https://manifest.test/1080p_60fps.m3u8",
			Bandwidth:  4500000,
			Width:      1920,
			Height:     1080,
			Codecs:     "avc1.640028,mp4a.40.2",
			FrameRate:  "60.000",
			AudioGroup: "audio-en",
		}
		// Refreshed master has a 30fps candidate with the original bandwidth,
		// and a 60fps candidate with different bandwidth.
		master := &HLSMaster{
			Variants: []HLSVariant{
				{
					URL:        "https://manifest.test/new_1080p_30fps.m3u8",
					Bandwidth:  4500000, // matches bandwidth, but wrong frame rate
					Width:      1920,
					Height:     1080,
					Codecs:     "avc1.640028,mp4a.40.2",
					FrameRate:  "30.000",
					AudioGroup: "audio-en",
				},
				{
					URL:        "https://manifest.test/new_1080p_60fps.m3u8",
					Bandwidth:  5000000, // different bandwidth, but correct frame rate
					Width:      1920,
					Height:     1080,
					Codecs:     "avc1.640028,mp4a.40.2",
					FrameRate:  "60.000",
					AudioGroup: "audio-en",
				},
			},
		}

		matched, err := MatchRefreshedVariant(orig60, master)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched.URL != "https://manifest.test/new_1080p_60fps.m3u8" {
			t.Fatalf("selected %q instead of 60fps candidate; bandwidth must not override frame rate", matched.URL)
		}
	})

	t.Run("matches variant with video range", func(t *testing.T) {
		origHDR := HLSVariant{
			URL:        "https://manifest.test/4k_hdr.m3u8",
			Width:      3840,
			Height:     2160,
			Codecs:     "avc1.640028,mp4a.40.2",
			VideoRange: "PQ",
			AudioGroup: "audio-en",
		}
		master := &HLSMaster{
			Variants: []HLSVariant{
				{
					URL:        "https://manifest.test/new_4k_sdr.m3u8",
					Width:      3840,
					Height:     2160,
					Codecs:     "avc1.640028,mp4a.40.2",
					VideoRange: "SDR",
					AudioGroup: "audio-en",
				},
				{
					URL:        "https://manifest.test/new_4k_pq.m3u8",
					Width:      3840,
					Height:     2160,
					Codecs:     "avc1.640028,mp4a.40.2",
					VideoRange: "PQ",
					AudioGroup: "audio-en",
				},
			},
		}

		matched, err := MatchRefreshedVariant(origHDR, master)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched.URL != "https://manifest.test/new_4k_pq.m3u8" {
			t.Fatalf("got URL %q, want PQ variant", matched.URL)
		}
	})

	t.Run("rejects variant with missing or mismatched video range", func(t *testing.T) {
		origHDR := HLSVariant{
			URL:        "https://manifest.test/4k_hdr.m3u8",
			Width:      3840,
			Height:     2160,
			Codecs:     "avc1.640028,mp4a.40.2",
			VideoRange: "PQ",
			AudioGroup: "audio-en",
		}
		master := &HLSMaster{
			Variants: []HLSVariant{
				{
					URL:        "https://manifest.test/new_4k_sdr.m3u8",
					Width:      3840,
					Height:     2160,
					Codecs:     "avc1.640028,mp4a.40.2",
					VideoRange: "SDR",
					AudioGroup: "audio-en",
				},
			},
		}

		_, err := MatchRefreshedVariant(origHDR, master)
		if err == nil {
			t.Fatal("expected error when video range is mismatched (PQ vs SDR), got nil")
		}

		masterNoRange := &HLSMaster{
			Variants: []HLSVariant{
				{
					URL:        "https://manifest.test/new_4k_no_range.m3u8",
					Width:      3840,
					Height:     2160,
					Codecs:     "avc1.640028,mp4a.40.2",
					VideoRange: "",
					AudioGroup: "audio-en",
				},
			},
		}

		_, err = MatchRefreshedVariant(origHDR, masterNoRange)
		if err == nil {
			t.Fatal("expected error when candidate lacks video range, got nil")
		}
	})

	t.Run("does not use bandwidth to select between different video ranges", func(t *testing.T) {
		origHDR := HLSVariant{
			URL:        "https://manifest.test/4k_hdr.m3u8",
			Bandwidth:  8000000,
			Width:      3840,
			Height:     2160,
			Codecs:     "avc1.640028,mp4a.40.2",
			VideoRange: "PQ",
			AudioGroup: "audio-en",
		}
		master := &HLSMaster{
			Variants: []HLSVariant{
				{
					URL:        "https://manifest.test/new_4k_sdr.m3u8",
					Bandwidth:  8000000, // matches bandwidth, but SDR
					Width:      3840,
					Height:     2160,
					Codecs:     "avc1.640028,mp4a.40.2",
					VideoRange: "SDR",
					AudioGroup: "audio-en",
				},
				{
					URL:        "https://manifest.test/new_4k_pq.m3u8",
					Bandwidth:  12000000, // different bandwidth, but correct PQ
					Width:      3840,
					Height:     2160,
					Codecs:     "avc1.640028,mp4a.40.2",
					VideoRange: "PQ",
					AudioGroup: "audio-en",
				},
			},
		}

		matched, err := MatchRefreshedVariant(origHDR, master)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched.URL != "https://manifest.test/new_4k_pq.m3u8" {
			t.Fatalf("selected %q instead of PQ candidate; bandwidth must not override video range", matched.URL)
		}
	})

	t.Run("rejects missing variant", func(t *testing.T) {
		master := &HLSMaster{
			Variants: []HLSVariant{
				{
					URL:        "https://manifest.test/720p.m3u8",
					Bandwidth:  2500000,
					Width:      1280,
					Height:     720,
					Codecs:     "avc1.4d401f,mp4a.40.2",
					AudioGroup: "audio-en",
				},
			},
		}

		_, err := MatchRefreshedVariant(orig, master)
		if err == nil {
			t.Fatal("expected error for missing variant, got nil")
		}
	})
}

func TestMatchRefreshedAudioRendition(t *testing.T) {
	orig := HLSAudioRendition{
		GroupID:    "audio-en",
		Name:       "English (Original)",
		Language:   "en",
		Default:    true,
		AutoSelect: true,
		URL:        "https://manifest.test/audio_en.m3u8",
	}

	t.Run("matches rendition by metadata within group", func(t *testing.T) {
		master := &HLSMaster{
			Audio: []HLSAudioRendition{
				{
					GroupID:    "audio-es",
					Name:       "Spanish",
					Language:   "es",
					Default:    false,
					AutoSelect: false,
					URL:        "https://manifest.test/audio_es.m3u8",
				},
				{
					GroupID:    "audio-en-refreshed",
					Name:       "English (Original)",
					Language:   "en",
					Default:    true,
					AutoSelect: true,
					URL:        "https://manifest.test/new_audio_en.m3u8",
				},
			},
		}

		matched, err := MatchRefreshedAudioRendition(orig, master, "audio-en-refreshed")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched.URL != "https://manifest.test/new_audio_en.m3u8" {
			t.Fatalf("got URL %q, want new audio", matched.URL)
		}
	})

	t.Run("scoped matching with changed group ID", func(t *testing.T) {
		master := &HLSMaster{
			Audio: []HLSAudioRendition{
				{
					GroupID:    "group-v2-main",
					Name:       "English (Original)",
					Language:   "en",
					Default:    true,
					AutoSelect: true,
					URL:        "https://manifest.test/v2_main_audio.m3u8",
				},
				{
					GroupID:    "group-v2-alt",
					Name:       "English (Original)",
					Language:   "en",
					Default:    true,
					AutoSelect: true,
					URL:        "https://manifest.test/v2_alt_audio.m3u8",
				},
			},
		}

		// When linked to group-v2-main via matched variant
		matched, err := MatchRefreshedAudioRendition(orig, master, "group-v2-main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched.URL != "https://manifest.test/v2_main_audio.m3u8" {
			t.Fatalf("got URL %q, want v2_main audio", matched.URL)
		}
	})

	t.Run("rejects ambiguous Default across multiple groups when unscoped", func(t *testing.T) {
		defaultRendition := HLSAudioRendition{
			GroupID:    "unknown-old-group",
			Name:       "Default",
			Language:   "en",
			Default:    true,
			AutoSelect: true,
			URL:        "https://manifest.test/old_default.m3u8",
		}
		master := &HLSMaster{
			Audio: []HLSAudioRendition{
				{
					GroupID:    "group-1",
					Name:       "Default",
					Language:   "en",
					Default:    true,
					AutoSelect: true,
					URL:        "https://manifest.test/group1_default.m3u8",
				},
				{
					GroupID:    "group-2",
					Name:       "Default",
					Language:   "en",
					Default:    true,
					AutoSelect: true,
					URL:        "https://manifest.test/group2_default.m3u8",
				},
			},
		}

		_, err := MatchRefreshedAudioRendition(defaultRendition, master, "")
		if err == nil {
			t.Fatal("expected ambiguity error when matching Default across multiple groups, got nil")
		}
	})

	t.Run("rejects missing audio rendition", func(t *testing.T) {
		master := &HLSMaster{
			Audio: []HLSAudioRendition{
				{
					GroupID:    "audio-es",
					Name:       "Spanish",
					Language:   "es",
					Default:    false,
					AutoSelect: false,
					URL:        "https://manifest.test/audio_es.m3u8",
				},
			},
		}

		_, err := MatchRefreshedAudioRendition(orig, master, "audio-es")
		if err == nil {
			t.Fatal("expected error for missing audio rendition, got nil")
		}
	})
}

func TestIsRefreshTrigger(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"ErrResourceExpired", ErrResourceExpired, true},
		{"wrapped ErrResourceExpired", fmt.Errorf("wrap: %w", ErrResourceExpired), true},
		{"HTTP 403", &HTTPStatusError{StatusCode: 403, Status: "403 Forbidden"}, true},
		{"HTTP 410", &HTTPStatusError{StatusCode: 410, Status: "410 Gone"}, true},
		{"wrapped HTTP 403 in ExecutionError", &ExecutionError{Stage: "download", Err: &HTTPStatusError{StatusCode: 403}}, true},
		{"HTTP 404", &HTTPStatusError{StatusCode: 404, Status: "404 Not Found"}, false},
		{"HTTP 429", &HTTPStatusError{StatusCode: 429, Status: "429 Too Many Requests"}, false},
		{"HTTP 500", &HTTPStatusError{StatusCode: 500, Status: "500 Internal Server Error"}, false},
		{"context.Canceled", context.Canceled, false},
		{"context.DeadlineExceeded", context.DeadlineExceeded, false},
		{"ErrDownloadStalled", ErrDownloadStalled, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsRefreshTrigger(tc.err)
			if got != tc.want {
				t.Fatalf("IsRefreshTrigger(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestDownloadChangedURLRestartsWithoutAppendingOldPartial(t *testing.T) {
	const content1 = "first-url-partial-data-old"
	const content2 = "second-url-complete-fresh-data"

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"etag1"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content1)))
		// Send only first 10 bytes and disconnect
		fmt.Fprint(w, content1[:10])
	}))
	defer server1.Close()

	var server2Requests atomic.Int32
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server2Requests.Add(1)
		if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
			t.Errorf("unexpected Range header on refreshed URL: %q", rangeHeader)
		}
		w.Header().Set("ETag", `"etag2"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content2)))
		fmt.Fprint(w, content2)
	}))
	defer server2.Close()

	destPath := filepath.Join(t.TempDir(), "output.bin")

	downloader := NewDownloader(server1.Client())

	// Download from URL1 (fails due to incomplete transfer, leaving .part)
	_, err := downloader.Download(
		context.Background(),
		Resource{URL: server1.URL},
		destPath,
		DownloadOptions{Resume: true, MaxRetries: 0},
	)
	if err == nil {
		t.Fatal("expected error on incomplete transfer 1")
	}

	// Verify partial file exists from attempt 1
	partInfo, err := os.Stat(destPath + ".part")
	if err != nil || partInfo.Size() != 10 {
		t.Fatalf("expected 10 bytes in partial file, got err=%v info=%+v", err, partInfo)
	}

	// Now download from URL2 with the same destination path
	downloader2 := NewDownloader(server2.Client())
	res, err := downloader2.Download(
		context.Background(),
		Resource{URL: server2.URL},
		destPath,
		DownloadOptions{Resume: true, MaxRetries: 0},
	)
	if err != nil {
		t.Fatalf("unexpected error downloading from URL2: %v", err)
	}

	if res.SizeBytes != int64(len(content2)) {
		t.Fatalf("got size %d, want %d", res.SizeBytes, len(content2))
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content2 {
		t.Fatalf("file content = %q; want %q", string(data), content2)
	}
}

func TestDownloadSameResourceETagResumePreserved(t *testing.T) {
	const content = "0123456789abcdef"
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"stable-strong-etag"`)
		switch requests.Add(1) {
		case 1:
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
			fmt.Fprint(w, content[:6])
		case 2:
			if got := r.Header.Get("Range"); got != "bytes=6-" {
				t.Errorf("Range = %q, want bytes=6-", got)
			}
			if got := r.Header.Get("If-Range"); got != `"stable-strong-etag"` {
				t.Errorf("If-Range = %q, want stable-strong-etag", got)
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 6-%d/%d", len(content)-1, len(content)))
			w.WriteHeader(http.StatusPartialContent)
			fmt.Fprint(w, content[6:])
		default:
			t.Errorf("unexpected request %d", requests.Load())
		}
	}))
	defer server.Close()

	destPath := filepath.Join(t.TempDir(), "output.bin")
	downloader := NewDownloader(server.Client())

	res, err := downloader.Download(
		context.Background(),
		Resource{URL: server.URL},
		destPath,
		DownloadOptions{Resume: true, MaxRetries: 1},
	)
	if err != nil {
		t.Fatalf("unexpected error on resume: %v", err)
	}

	if res.SizeBytes != int64(len(content)) {
		t.Fatalf("size = %d, want %d", res.SizeBytes, len(content))
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("content = %q, want %q", string(data), content)
	}
}

func TestResolveRefreshedMaster(t *testing.T) {
	masterM3U8 := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-en",NAME="English (Original)",DEFAULT=YES,AUTOSELECT=YES,LANGUAGE="en",URI="audio_en.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=4500000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio-en"
video_1080p.m3u8
`
	videoM3U8 := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:5
#EXTINF:5.0,
video0.ts
#EXT-X-ENDLIST
`
	audioM3U8 := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:5
#EXTINF:5.0,
audio0.ts
#EXT-X-ENDLIST
`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, masterM3U8)
		case "/video_1080p.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, videoM3U8)
		case "/audio_en.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, audioM3U8)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	processor, err := NewFFmpeg("")
	if err != nil {
		t.Skip("ffmpeg not available:", err)
	}
	hlsDownloader, err := NewHLSDownloader(server.Client(), processor)
	if err != nil {
		t.Fatalf("unexpected error creating downloader: %v", err)
	}

	origVariant := HLSVariant{
		Width:      1920,
		Height:     1080,
		Codecs:     "avc1.640028,mp4a.40.2",
		AudioGroup: "audio-en",
	}
	origAudio := &HLSAudioRendition{
		GroupID:    "audio-en",
		Name:       "English (Original)",
		Language:   "en",
		Default:    true,
		AutoSelect: true,
	}

	resolved, err := hlsDownloader.ResolveRefreshedMaster(
		context.Background(),
		Resource{URL: server.URL + "/master.m3u8"},
		origVariant,
		origAudio,
	)
	if err != nil {
		t.Fatalf("unexpected error resolving refreshed master: %v", err)
	}

	if resolved.Video == nil || len(resolved.Video.Playlist.Segments) != 1 {
		t.Fatalf("unexpected resolved video track: %+v", resolved.Video)
	}
	if resolved.Audio == nil || len(resolved.Audio.Playlist.Segments) != 1 {
		t.Fatalf("unexpected resolved audio track: %+v", resolved.Audio)
	}
	if resolved.SelectedVariant == nil || resolved.SelectedVariant.Height != 1080 {
		t.Fatalf("unexpected selected variant: %+v", resolved.SelectedVariant)
	}
	if resolved.SelectedAudio == nil || resolved.SelectedAudio.Language != "en" {
		t.Fatalf("unexpected selected audio: %+v", resolved.SelectedAudio)
	}
}

func TestResolveRefreshedAudioOnlyMaster(t *testing.T) {
	masterM3U8 := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-en",NAME="English",DEFAULT=YES,AUTOSELECT=YES,LANGUAGE="en",URI="audio_en.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=4500000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio-en"
video_1080p.m3u8
`
	audioM3U8 := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:5
#EXTINF:5.0,
audio0.ts
#EXT-X-ENDLIST
`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, masterM3U8)
		case "/audio_en.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, audioM3U8)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	processor, err := NewFFmpeg("")
	if err != nil {
		t.Skip("ffmpeg not available:", err)
	}
	hlsDownloader, err := NewHLSDownloader(server.Client(), processor)
	if err != nil {
		t.Fatalf("unexpected error creating downloader: %v", err)
	}

	origAudio := HLSAudioRendition{
		GroupID:    "audio-en",
		Name:       "English",
		Language:   "en",
		Default:    true,
		AutoSelect: true,
	}

	track, selectedAudio, err := hlsDownloader.ResolveRefreshedAudioOnlyMaster(
		context.Background(),
		Resource{URL: server.URL + "/master.m3u8"},
		origAudio,
	)
	if err != nil {
		t.Fatalf("unexpected error resolving audio master: %v", err)
	}

	if track == nil || len(track.Playlist.Segments) != 1 {
		t.Fatalf("unexpected audio track: %+v", track)
	}
	if selectedAudio == nil || selectedAudio.Language != "en" {
		t.Fatalf("unexpected selected audio: %+v", selectedAudio)
	}
}

func TestDownloadPreservesExistingDestinationOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	destPath := filepath.Join(t.TempDir(), "destination.mp4")
	originalContent := "existing user file content that must not be overwritten"
	if err := os.WriteFile(destPath, []byte(originalContent), 0644); err != nil {
		t.Fatal(err)
	}

	downloader := NewDownloader(server.Client())
	_, err := downloader.Download(
		context.Background(),
		Resource{URL: server.URL},
		destPath,
		DownloadOptions{MaxRetries: 0},
	)
	if err == nil {
		t.Fatal("expected download error on 403 Forbidden")
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("destination file disappeared: %v", err)
	}
	if string(data) != originalContent {
		t.Fatalf("destination file content altered; got %q, want %q", string(data), originalContent)
	}
}

func TestHLSPlaylist403_SuccessfulRefresh(t *testing.T) {
	masterM3U8 := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-v2",NAME="English",DEFAULT=YES,AUTOSELECT=YES,LANGUAGE="en",URI="audio.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=4500000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",FRAME-RATE="30.000",VIDEO-RANGE="SDR",AUDIO="audio-v2"
video.m3u8
`
	videoM3U8 := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:5
#EXTINF:5.0,
video0.ts
#EXT-X-ENDLIST
`
	audioM3U8 := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:5
#EXTINF:5.0,
audio0.ts
#EXT-X-ENDLIST
`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master_expired.m3u8":
			w.WriteHeader(http.StatusForbidden)
		case "/master_refreshed.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, masterM3U8)
		case "/video.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, videoM3U8)
		case "/audio.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, audioM3U8)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	processor, err := NewFFmpeg("")
	if err != nil {
		t.Skip("ffmpeg not available:", err)
	}
	hlsDownloader, err := NewHLSDownloader(server.Client(), processor)
	if err != nil {
		t.Fatalf("unexpected error creating downloader: %v", err)
	}

	// 1. Initial resolution of expired manifest fails with 403 retaining HTTPStatusError
	_, initialErr := hlsDownloader.ResolvePlaylistLanguage(
		context.Background(),
		Resource{URL: server.URL + "/master_expired.m3u8"},
		1080,
		"en",
	)
	if initialErr == nil {
		t.Fatal("expected error on 403 expired master playlist, got nil")
	}
	if !IsRefreshTrigger(initialErr) {
		t.Fatalf("expected initial error %v to be recognized as a refresh trigger", initialErr)
	}

	// 2. Refresh occurs: ResolveRefreshedMaster succeeds on the refreshed master URL
	origVariant := HLSVariant{
		Width:      1920,
		Height:     1080,
		Codecs:     "avc1.640028,mp4a.40.2",
		FrameRate:  "30.000",
		VideoRange: "SDR",
		AudioGroup: "audio-v1", // Note: group ID changed from v1 to v2 in refreshed manifest
	}
	origAudio := &HLSAudioRendition{
		GroupID:    "audio-v1",
		Name:       "English",
		Language:   "en",
		Default:    true,
		AutoSelect: true,
	}

	resolved, err := hlsDownloader.ResolveRefreshedMaster(
		context.Background(),
		Resource{URL: server.URL + "/master_refreshed.m3u8"},
		origVariant,
		origAudio,
	)
	if err != nil {
		t.Fatalf("unexpected error resolving refreshed master: %v", err)
	}

	if resolved.SelectedVariant == nil || resolved.SelectedVariant.Height != 1080 {
		t.Fatalf("unexpected variant after refresh: %+v", resolved.SelectedVariant)
	}
	if resolved.SelectedAudio == nil || resolved.SelectedAudio.Language != "en" {
		t.Fatalf("unexpected audio after refresh: %+v", resolved.SelectedAudio)
	}
	if resolved.SelectedAudio.GroupID != "audio-v2" {
		t.Fatalf("expected refreshed audio group 'audio-v2', got %q", resolved.SelectedAudio.GroupID)
	}
}
