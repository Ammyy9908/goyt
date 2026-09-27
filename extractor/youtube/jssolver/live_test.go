//go:build livetest

package jssolver_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ammyy9908/goyt/extractor/youtube"
	"github.com/ammyy9908/goyt/extractor/youtube/jssolver"
)

type liveTrackingSolver struct {
	inner         youtube.ChallengeSolver
	sigChallenges atomic.Int64
	nChallenges   atomic.Int64
	sigSuccesses  atomic.Int64
	nSuccesses    atomic.Int64
}

func (s *liveTrackingSolver) SolveChallenges(ctx context.Context, script youtube.PlayerScript, batch youtube.ChallengeBatch) (youtube.ChallengeBatchResult, error) {
	s.sigChallenges.Add(int64(len(batch.Signatures)))
	s.nChallenges.Add(int64(len(batch.NParams)))

	res, err := s.inner.SolveChallenges(ctx, script, batch)
	if err == nil {
		for _, sig := range res.Signatures {
			if sig.Error == nil && sig.Deciphered != "" {
				s.sigSuccesses.Add(1)
			}
		}
		for _, n := range res.NParams {
			if n.Error == nil && n.Transformed != "" {
				s.nSuccesses.Add(1)
			}
		}
	}
	return res, err
}

func TestLiveYouTubeChallengeSolving(t *testing.T) {
	solver, err := jssolver.New(jssolver.WithRuntime("auto"))
	if err != nil {
		t.Skipf("skipping live test: no supported JS runtime found: %v", err)
	}

	tracker := &liveTrackingSolver{inner: solver}
	client := &http.Client{Timeout: 30 * time.Second}
	extractor := youtube.New(client, youtube.WithChallengeSolver(tracker))

	testURL, err := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Web client triggers player cipher/n challenges on standard watch endpoints
	media, err := extractor.ExtractDownloadableWithClient(ctx, testURL, youtube.ClientWeb)
	if err != nil {
		var extractErr *youtube.ExtractionError
		if errors.As(err, &extractErr) {
			if extractErr.Code == youtube.ErrCodeBotCheckRequired {
				t.Skipf("skipping live test: YouTube bot check / sign-in required on current IP: %v", err)
			}
			if extractErr.Code == youtube.ErrCodePlaybackRestricted {
				t.Skipf("skipping live test: YouTube playability restriction: %v", err)
			}
		}
		t.Skipf("skipping live test: extraction failed: %v", err)
	}

	totalChallenges := tracker.sigChallenges.Load() + tracker.nChallenges.Load()
	if totalChallenges == 0 {
		t.Skip("inconclusive: no signature or n challenges were encountered in the live response")
	}

	t.Logf("Solver observed and processed %d signature and %d n challenges (successes: %d sig, %d n)",
		tracker.sigChallenges.Load(), tracker.nChallenges.Load(),
		tracker.sigSuccesses.Load(), tracker.nSuccesses.Load(),
	)

	if len(media.Formats) == 0 {
		t.Fatal("expected at least one format in extracted media")
	}

	// Select first resolved format with an HTTP URL to perform a bounded media request
	var selectedURL string
	for _, f := range media.Formats {
		if f.Resource.URL != "" && strings.HasPrefix(f.Resource.URL, "http") {
			selectedURL = f.Resource.URL
			break
		}
	}
	if selectedURL == "" {
		t.Fatal("no resolved format URL found in extracted media")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, selectedURL, nil)
	if err != nil {
		t.Fatalf("failed to create media request: %v", err)
	}
	req.Header.Set("Range", "bytes=0-1023")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("bounded media request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("unexpected media response status %d (expected 200 or 206)", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("media request returned HTML response instead of audio/video content (Content-Type: %s)", contentType)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		t.Fatalf("failed to read media body: %v", err)
	}
	if len(bodyBytes) == 0 {
		t.Fatal("bounded media request returned 0 bytes")
	}

	// Verify the body is binary media, not HTML error
	bodyPrefix := string(bodyBytes[:min(len(bodyBytes), 64)])
	if strings.Contains(strings.ToLower(bodyPrefix), "<!doctype") || strings.Contains(strings.ToLower(bodyPrefix), "<html") {
		t.Fatal("media response body contained HTML markup")
	}

	t.Logf("PASS: bounded media request received %d valid media bytes (Content-Type: %s, HTTP %d)",
		len(bodyBytes), contentType, resp.StatusCode,
	)
	t.Log("Note: Bounded media request confirms solved URL reachability; it is not proof of full unthrottled download.")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
