package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/ammyy9908/goyt"
)

const playerEndpoint = "https://www.youtube.com/youtubei/v1/player?prettyPrint=false"

type playerRequest struct {
	Context struct {
		Client clientContext `json:"client"`
	} `json:"context"`

	VideoID        string `json:"videoId"`
	ContentCheckOK bool   `json:"contentCheckOk"`
	RacyCheckOK    bool   `json:"racyCheckOk"`

	PlaybackContext struct {
		ContentPlaybackContext struct {
			HTML5Preference string `json:"html5Preference"`
		} `json:"contentPlaybackContext"`
	} `json:"playbackContext"`
}

func (e *Extractor) InspectVisionOS(
	ctx context.Context,
	u *url.URL,
) (*Report, error) {
	return e.inspectVisionOS(ctx, u)
}

func (e *Extractor) inspectVisionOS(
	ctx context.Context,
	u *url.URL,
) (*Report, error) {
	id, err := videoID(u)
	if err != nil {
		return nil, err
	}

	visitorData, err := e.fetchVisitorData(ctx, id)
	if err != nil {
		return nil, err
	}

	player, err := e.requestPlayer(
		ctx,
		id,
		visionOSProfile(),
		visitorData,
	)
	if err != nil {
		return nil, err
	}

	report := buildReport(id, player)

	if player.StreamingData.HLSManifestURL != "" {
		if manifestResource, err := youtubeManifestResource(
			player.StreamingData.HLSManifestURL,
			visionOSProfile(),
		); err == nil {
			if hlsReq, err := http.NewRequestWithContext(
				ctx,
				http.MethodGet,
				manifestResource.URL,
				nil,
			); err == nil {
				hlsReq.Header = manifestResource.Headers.Clone()
				if hlsResp, err := e.client.Do(hlsReq); err == nil {
					defer hlsResp.Body.Close()
					if hlsData, err := io.ReadAll(
						io.LimitReader(hlsResp.Body, 2<<20),
					); err == nil && hlsResp.StatusCode == http.StatusOK {
						if parsedURL, err := url.Parse(manifestResource.URL); err == nil {
							if master, err := goyt.ParseHLSMaster(hlsData, parsedURL); err == nil {
								for _, a := range master.Audio {
									report.HLSRenditions = append(
										report.HLSRenditions,
										DiscoveredHLSRendition{
											GroupID:    a.GroupID,
											Name:       a.Name,
											Language:   a.Language,
											Default:    a.Default,
											AutoSelect: a.AutoSelect,
											IsOriginal: isOriginalAudioName(a.Name),
										},
									)
								}
							}
						}
					}
				}
			}
		}
	}

	// buildReport's first limitation describes watch-page inspection.
	// Replace it because this result came from an API request.
	if len(report.Limitations) > 0 {
		report.Limitations[0] =
			"VISIONOS player API response; discovered URLs are not download-verified."
	}

	report.Limitations = append(
		report.Limitations,
		"Visitor identifier propagated from a fresh watch page; no account cookies or PO-token provider.",
	)

	return report, nil
}

func (e *Extractor) requestPlayer(
	ctx context.Context,
	id string,
	profile ClientProfile,
	visitorData ...string,
) (*playerResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if !videoIDPattern.MatchString(id) {
		return nil, errors.New("youtube: invalid video ID")
	}

	payload := playerRequest{
		VideoID:        id,
		ContentCheckOK: true,
		RacyCheckOK:    true,
	}

	payload.Context.Client = profile.Context

	payload.PlaybackContext.ContentPlaybackContext.HTML5Preference =
		"HTML5_PREF_WANTS"

	if len(visitorData) > 1 {
		return nil, errors.New("youtube: expected at most one visitor identifier")
	}

	visitor := ""
	if len(visitorData) == 1 {
		visitor = visitorData[0]
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		playerEndpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set(
		"X-YouTube-Client-Version",
		profile.Context.ClientVersion,
	)

	if visitor != "" {
		req.Header.Set("X-Goog-Visitor-Id", visitor)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://www.youtube.com")
	req.Header.Set("User-Agent", profile.Context.UserAgent)
	req.Header.Set("X-YouTube-Client-Name", profile.HeaderID)
	req.Header.Set(
		"X-YouTube-Client-Version",
		profile.Context.ClientVersion,
	)

	// Preserve the configured transport and timeout, but do not inherit
	// account cookies or follow redirects for this API request.
	client := *e.client
	client.Jar = nil
	client.CheckRedirect = func(
		*http.Request,
		[]*http.Request,
	) error {
		return http.ErrUseLastResponse
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf(
			"youtube: %s player request: %w",
			profile.Name,
			err,
		)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read only a bounded error response. Do not print the raw body,
		// request headers, cookies, or signed URLs.
		body, readErr := io.ReadAll(
			io.LimitReader(resp.Body, 64*1024),
		)
		if readErr != nil {
			return nil, fmt.Errorf(
				"youtube: %s player API returned HTTP %d; reading error response: %w",
				profile.Name,
				resp.StatusCode,
				readErr,
			)
		}

		var apiError struct {
			Error struct {
				Code    int    `json:"code"`
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"error"`
		}

		if json.Unmarshal(body, &apiError) == nil &&
			apiError.Error.Message != "" {
			message := apiError.Error.Message
			if len(message) > 2000 {
				message = message[:2000] + "..."
			}

			return nil, fmt.Errorf(
				"youtube: %s player API returned HTTP %d (%s): %q",
				profile.Name,
				resp.StatusCode,
				apiError.Error.Status,
				message,
			)
		}

		return nil, fmt.Errorf(
			"youtube: %s player API returned HTTP %d without a recognized JSON error",
			profile.Name,
			resp.StatusCode,
		)
	}

	data, err := io.ReadAll(
		io.LimitReader(resp.Body, maxPageBytes+1),
	)
	if err != nil {
		return nil, fmt.Errorf("youtube: read player response: %w", err)
	}

	if len(data) > maxPageBytes {
		return nil, errors.New("youtube: player response exceeds size limit")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var player playerResponse
	if err := json.Unmarshal(data, &player); err != nil {
		return nil, errors.New(
			"youtube: player API returned invalid JSON",
		)
	}

	status := player.PlayabilityStatus.Status
	if status == "" {
		return nil, errors.New(
			"youtube: player API response is missing playback status",
		)
	}

	returnedID := player.VideoDetails.VideoID

	if returnedID != "" && returnedID != id {
		return nil, errors.New(
			"youtube: player API returned a different video ID",
		)
	}

	if status == "OK" && returnedID != id {
		return nil, errors.New(
			"youtube: playable response is missing the requested video ID",
		)
	}

	// Restricted/error playback responses are returned for inspection.
	// They are not treated as downloadable results.
	return &player, nil
}
