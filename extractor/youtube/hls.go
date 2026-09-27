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

// ExtractHLS discovers an HLS manifest using the default client (visionos).
func (e *Extractor) ExtractHLS(
	ctx context.Context,
	u *url.URL,
) (*HLSExtraction, error) {
	return e.ExtractHLSWithClient(ctx, u, DefaultClient)
}

// ExtractHLSWithClient discovers an HLS manifest for the specified client.
//
// Finding a manifest does not establish that all its features are supported.
func (e *Extractor) ExtractHLSWithClient(
	ctx context.Context,
	u *url.URL,
	clientName string,
) (*HLSExtraction, error) {
	clientName, err := ValidateClient(clientName)
	if err != nil {
		return nil, err
	}

	id, err := videoID(u)
	if err != nil {
		return nil, err
	}

	profile, _ := GetClientProfile(clientName)

	var player *playerResponse

	if clientName == ClientWeb {
		player, err = e.fetchWatchPagePlayer(ctx, id)
		if err != nil {
			return nil, classifyExtractionFailure(clientName, err)
		}
	} else {
		visitor, vErr := e.fetchVisitorData(ctx, id)
		if vErr != nil {
			return nil, classifyExtractionFailure(clientName, vErr)
		}

		player, err = e.requestPlayer(
			ctx,
			id,
			profile,
			visitor,
		)
		if err != nil {
			return nil, classifyExtractionFailure(clientName, err)
		}
	}

	if player.PlayabilityStatus.Status != "OK" {
		return nil, classifyPlayabilityError(
			clientName,
			player.PlayabilityStatus.Status,
			player.PlayabilityStatus.Reason,
		)
	}

	if player.VideoDetails.IsLiveContent {
		return nil, &ExtractionError{
			Code:           ErrCodeNoSupportedFormats,
			Client:         clientName,
			PlaybackStatus: player.PlayabilityStatus.Status,
			Message:        "youtube: live-related videos are not supported by this prototype",
		}
	}

	rawURL := player.StreamingData.HLSManifestURL
	if rawURL == "" {
		return nil, &ExtractionError{
			Code:    ErrCodeManifestUnavailable,
			Client:  clientName,
			Message: fmt.Sprintf("youtube: %s player response did not expose an HLS manifest", clientName),
		}
	}

	manifest, err := youtubeManifestResource(rawURL, profile)
	if err != nil {
		if strings.Contains(err.Error(), "n parameter") {
			return nil, &ExtractionError{
				Code:    ErrCodeNChallengeReq,
				Client:  clientName,
				Message: fmt.Sprintf("youtube: %s HLS manifest requires an unsupported n-challenge", clientName),
				Err:     err,
			}
		}
		return nil, &ExtractionError{
			Code:    ErrCodeInvalidPlayerResponse,
			Client:  clientName,
			Message: fmt.Sprintf("youtube: %s invalid HLS manifest: %v", clientName, err),
			Err:     err,
		}
	}

	return &HLSExtraction{
		Media:    buildReport(id, player).Media,
		Manifest: manifest,
	}, nil
}

func youtubeManifestResource(
	rawURL string,
	profile ClientProfile,
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

	userAgent := profile.Context.UserAgent
	if userAgent == "" {
		userAgent = "Mozilla/5.0"
	}

	resource := goyt.Resource{
		URL: rawURL,
		Headers: http.Header{
			"User-Agent": []string{userAgent},
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
