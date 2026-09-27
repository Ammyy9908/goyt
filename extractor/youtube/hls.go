package youtube

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ammyy9908/goyt"
)

type HLSExtraction struct {
	Media    *goyt.Media
	Manifest goyt.Resource
}

// ExtractHLS discovers an HLS manifest without downloading its segments.
//
// This prototype supports public, non-live-related videos.
// Finding a manifest does not establish that all its features are supported.
func (e *Extractor) ExtractHLS(
	ctx context.Context,
	u *url.URL,
) (*HLSExtraction, error) {
	id, err := videoID(u)
	if err != nil {
		return nil, err
	}

	visitor, err := e.fetchVisitorData(ctx, id)
	if err != nil {
		return nil, err
	}

	profile := visionOSProfile()

	player, err := e.requestPlayer(
		ctx,
		id,
		profile,
		visitor,
	)
	if err != nil {
		return nil, err
	}

	if player.PlayabilityStatus.Status != "OK" {
		return nil, fmt.Errorf(
			"youtube: playback status %s: %s",
			player.PlayabilityStatus.Status,
			player.PlayabilityStatus.Reason,
		)
	}

	if player.VideoDetails.IsLiveContent {
		return nil, errors.New(
			"youtube: live-related videos are not supported by this prototype",
		)
	}

	rawURL := player.StreamingData.HLSManifestURL
	if rawURL == "" {
		return nil, errors.New(
			"youtube: this player response did not expose an HLS manifest",
		)
	}

	manifest, err := youtubeManifestResource(rawURL, profile)
	if err != nil {
		return nil, err
	}

	return &HLSExtraction{
		Media:    buildReport(id, player).Media,
		Manifest: manifest,
	}, nil
}

func youtubeManifestResource(
	rawURL string,
	profile clientProfile,
) (goyt.Resource, error) {
	u, err := url.Parse(rawURL)
	if err != nil ||
		u.Scheme != "https" ||
		u.Hostname() == "" ||
		u.User != nil ||
		u.Fragment != "" {
		return goyt.Resource{}, errors.New(
			"youtube: invalid HLS manifest URL",
		)
	}

	// YouTube can encode parameters in either the query or path.
	// Conservatively reject an exposed n parameter until challenge solving
	// is implemented. Absence does not guarantee playback will succeed.
	if u.Query().Get("n") != "" || strings.Contains(u.Path, "/n/") {
		return goyt.Resource{}, errors.New(
			"youtube: HLS manifest has an unresolved n parameter",
		)
	}

	resource := goyt.Resource{
		URL: rawURL,
		Headers: http.Header{
			"User-Agent": []string{profile.Context.UserAgent},
		},
	}

	expiry := u.Query().Get("expire")

	if expiry == "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == "expire" {
				expiry = parts[i+1]
				break
			}
		}
	}

	if seconds, err := strconv.ParseInt(expiry, 10, 64); err == nil &&
		seconds > 0 {
		expires := time.Unix(seconds, 0)
		resource.ExpiresAt = &expires
	}

	return resource, nil
}
