package youtube

import (
	"errors"
	"net/url"
	"testing"
)

func TestVideoID(t *testing.T) {
	for _, input := range []string{
		"https://www.youtube.com/watch?v=abcdefghijk",
		"https://youtu.be/abcdefghijk",
		"https://www.youtube.com/shorts/abcdefghijk",
	} {
		u, err := url.Parse(input)
		if err != nil {
			t.Fatal(err)
		}

		id, err := videoID(u)
		if err != nil || id != "abcdefghijk" {
			t.Fatalf("input %s: ID=%q err=%v", input, id, err)
		}
	}

	u, _ := url.Parse(
		"https://youtube.com.evil.test/watch?v=abcdefghijk",
	)

	if _, err := videoID(u); err == nil {
		t.Fatal("unrelated host accepted")
	}
}

func TestParseAndReport(t *testing.T) {
	page := []byte(`
		<html><script>
		var ytInitialPlayerResponse = {
			"videoDetails": {
				"videoId": "abcdefghijk",
				"title": "A title with } braces",
				"lengthSeconds": "60"
			},
			"playabilityStatus": {"status": "OK"},
			"streamingData": {
				"formats": [{
					"itag": 18,
					"mimeType": "video/mp4",
					"url": "https://media.test/video?n=challenge"
				}],
				"adaptiveFormats": [{
					"itag": 137,
					"mimeType": "video/mp4",
					"signatureCipher": "url=https%3A%2F%2Fmedia.test%2Fvideo&s=secret"
				}]
			}
		};
		</script></html>
	`)

	player, err := parsePlayer(page)
	if err != nil {
		t.Fatal(err)
	}

	report := buildReport("abcdefghijk", player)

	if report.Media.Title != "A title with } braces" {
		t.Fatal("nested JSON parsing failed")
	}

	if report.Media.Duration == nil ||
		report.Media.Duration.Seconds() != 60 {
		t.Fatal("incorrect duration")
	}

	if len(report.Formats) != 2 {
		t.Fatalf("got %d formats", len(report.Formats))
	}

	if !report.Formats[0].NChallenge ||
		!report.Formats[1].SignatureChallenge {
		t.Fatal("challenge information was lost")
	}

	if len(report.Media.Formats) != 0 {
		t.Fatal("unverified formats must not reach the download planner")
	}
}

func TestPlayerResponseMissing(t *testing.T) {
	_, err := parsePlayer([]byte("<html>consent page</html>"))

	if !errors.Is(err, ErrPlayerResponseMissing) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRestrictedResponse(t *testing.T) {
	player, err := parsePlayer([]byte(`
		ytInitialPlayerResponse = {
			"playabilityStatus": {
				"status": "LOGIN_REQUIRED",
				"reason": "Sign in required"
			}
		};
	`))
	if err != nil {
		t.Fatal(err)
	}

	report := buildReport("abcdefghijk", player)

	if report.PlaybackStatus != "LOGIN_REQUIRED" ||
		report.PlaybackReason != "Sign in required" {
		t.Fatal("playback restriction was lost")
	}
}

func TestAudioTracksReport(t *testing.T) {
	player := &playerResponse{}
	player.VideoDetails.VideoID = "abcdefghijk"
	player.VideoDetails.Title = "Test Video"
	player.PlayabilityStatus.Status = "OK"

	// Audio tracks from captions
	player.Captions.PlayerCaptionsTracklistRenderer.AudioTracks = []struct {
		CaptionTrackIndices []int  `json:"captionTrackIndices"`
		DefaultTrackIndex   int    `json:"defaultCaptionTrackIndex"`
		HasDefaultTrack     bool   `json:"hasDefaultTrack"`
		AudioIsDefault      bool   `json:"audioIsDefault"`
		ID                  string `json:"id"`
		DisplayName         string `json:"displayName"`
		Visibility          string `json:"visibility"`
	}{
		{ID: "es.10", DisplayName: "Spanish (dubbed)", AudioIsDefault: true},
		{ID: "en.4", DisplayName: "English (original)", AudioIsDefault: false},
	}

	// Audio tracks from adaptive formats
	player.StreamingData.AdaptiveFormats = []playerFormat{
		{
			Itag:     140,
			MIMEType: "audio/mp4",
			AudioTrack: &playerAudioTrack{
				ID:             "ja.4",
				DisplayName:    "Japanese [original]",
				AudioIsDefault: false,
			},
		},
	}

	report := buildReport("abcdefghijk", player)

	if len(report.AudioTracks) != 3 {
		t.Fatalf("got %d audio tracks; want 3", len(report.AudioTracks))
	}

	// Verify Spanish dubbed
	if report.AudioTracks[0].ID != "es.10" || report.AudioTracks[0].IsOriginal {
		t.Fatalf("unexpected Spanish track: %+v", report.AudioTracks[0])
	}

	// Verify English original
	if report.AudioTracks[1].ID != "en.4" || !report.AudioTracks[1].IsOriginal {
		t.Fatalf("unexpected English track: %+v", report.AudioTracks[1])
	}

	// Verify Japanese original
	if report.AudioTracks[2].ID != "ja.4" || !report.AudioTracks[2].IsOriginal {
		t.Fatalf("unexpected Japanese track: %+v", report.AudioTracks[2])
	}
}
