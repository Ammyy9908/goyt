package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type playerTransport func(*http.Request) (*http.Response, error)

func (f playerTransport) RoundTrip(
	req *http.Request,
) (*http.Response, error) {
	return f(req)
}

func playerHTTPResponse(
	req *http.Request,
	status int,
	body string,
) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: req,
	}
}

func TestVisionOSRequest(t *testing.T) {

	client := &http.Client{
		Transport: playerTransport(func(
			req *http.Request,
		) (*http.Response, error) {

			if req.Method == http.MethodGet {
				if req.URL.Host != "www.youtube.com" ||
					req.URL.Path != "/watch" {
					t.Fatalf("unexpected watch-page URL: %s", req.URL)
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"text/html; charset=utf-8"},
					},
					Body: io.NopCloser(strings.NewReader(
						`<script>ytcfg.set({"VISITOR_DATA":"test-visitor"});</script>`,
					)),
					Request: req,
				}, nil
			}
			if req.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", req.Method)
			}

			if req.URL.String() != playerEndpoint {
				t.Fatalf("unexpected endpoint: %s", req.URL)
			}

			if req.Header.Get("X-YouTube-Client-Name") != "101" ||
				req.Header.Get("X-YouTube-Client-Version") != "1.02" {
				t.Fatal("incorrect client headers")
			}

			if req.Header.Get("Cookie") != "" {
				t.Fatal("unexpected cookies")
			}

			var payload playerRequest
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}

			if payload.VideoID != "abcdefghijk" ||
				payload.Context.Client.ClientName != "VISIONOS" {
				t.Fatalf("incorrect payload: %+v", payload)
			}

			if payload.Context.Client.TimeZone != "UTC" ||
				payload.Context.Client.UTCOffsetMinutes != 0 {
				t.Fatal("incorrect timezone context")
			}

			if !payload.ContentCheckOK || !payload.RacyCheckOK {
				t.Fatal("missing player check fields")
			}

			if payload.PlaybackContext.ContentPlaybackContext.HTML5Preference !=
				"HTML5_PREF_WANTS" {
				t.Fatal("missing playback preference")
			}

			if req.Header.Get("User-Agent") !=
				payload.Context.Client.UserAgent {
				t.Fatal("header and context user agents differ")
			}

			return playerHTTPResponse(req, 200, `{
				"videoDetails": {
					"videoId": "abcdefghijk",
					"title": "Fixture video",
					"lengthSeconds": "10"
				},
				"playabilityStatus": {"status": "OK"},
				"streamingData": {
					"formats": [{
						"itag": 18,
						"mimeType": "video/mp4",
						"url": "https://media.test/video.mp4"
					}]
				}
			}`), nil
		}),
	}

	u, _ := url.Parse("https://youtu.be/abcdefghijk")

	report, err := New(client).InspectVisionOS(
		context.Background(),
		u,
	)
	if err != nil {
		t.Fatal(err)
	}

	if report.Media.Title != "Fixture video" ||
		len(report.Formats) != 1 ||
		!report.Formats[0].HasDirectURL {
		t.Fatalf("unexpected report: %+v", report)
	}

	if len(report.Media.Formats) != 0 {
		t.Fatal("unverified URLs must not enter the download planner")
	}
}

func TestPlayerResponseValidation(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "HTTP error",
			status: 429,
			body:   `{}`,
		},
		{
			name:   "invalid JSON",
			status: 200,
			body:   `<html>not JSON</html>`,
		},
		{
			name:   "missing status",
			status: 200,
			body:   `{}`,
		},
		{
			name:   "wrong video",
			status: 200,
			body: `{
				"playabilityStatus": {"status":"OK"},
				"videoDetails": {"videoId":"12345678901"}
			}`,
		},
		{
			name:   "missing playable video ID",
			status: 200,
			body:   `{"playabilityStatus":{"status":"OK"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{
				Transport: playerTransport(func(
					req *http.Request,
				) (*http.Response, error) {
					return playerHTTPResponse(
						req,
						tc.status,
						tc.body,
					), nil
				}),
			}

			_, err := New(client).requestPlayer(
				context.Background(),
				"abcdefghijk",
				visionOSProfile(),
			)

			if err == nil {
				t.Fatal("invalid response was accepted")
			}
		})
	}
}

func TestPlayerRestrictedResponse(t *testing.T) {
	client := &http.Client{
		Transport: playerTransport(func(
			req *http.Request,
		) (*http.Response, error) {
			if req.Method == http.MethodGet {
				if req.URL.Host != "www.youtube.com" ||
					req.URL.Path != "/watch" {
					t.Fatalf("unexpected watch-page URL: %s", req.URL)
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"text/html; charset=utf-8"},
					},
					Body: io.NopCloser(strings.NewReader(
						`<script>ytcfg.set({"VISITOR_DATA":"test-visitor"});</script>`,
					)),
					Request: req,
				}, nil
			}
			return playerHTTPResponse(req, 200, `{
				"playabilityStatus": {
					"status":"LOGIN_REQUIRED",
					"reason":"Sign in required"
				}
			}`), nil
		}),
	}

	u, _ := url.Parse("https://youtu.be/abcdefghijk")

	report, err := New(client).InspectVisionOS(
		context.Background(),
		u,
	)
	if err != nil {
		t.Fatal(err)
	}

	if report.PlaybackStatus != "LOGIN_REQUIRED" {
		t.Fatal("playback restriction was lost")
	}
}

func TestPlayerCanceledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := &http.Client{
		Transport: playerTransport(func(
			*http.Request,
		) (*http.Response, error) {
			t.Fatal("canceled request reached transport")
			return nil, errors.New("unexpected request")
		}),
	}

	_, err := New(client).requestPlayer(
		ctx,
		"abcdefghijk",
		visionOSProfile(),
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
