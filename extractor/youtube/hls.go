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
	Media            *goyt.Media
	Manifest         goyt.Resource
	ExtractionReport *ExtractionDiagnostics
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
	builder := newDiagBuilder(clientName, id)

	var player *playerResponse
	var playerReqDur time.Duration

	if clientName == ClientWeb {
		t0 := time.Now()
		player, err = e.fetchWatchPagePlayer(ctx, id)
		dur := time.Since(t0)
		if err != nil {
			builder.recordStage(StageWatchPageFetch, dur, 0, false, err)
			extErr := classifyExtractionFailure(clientName, err)
			diag := builder.finishFailure(StageWatchPageFetch, err, extErr)
			extErr.Diagnostics = diag
			e.setLatestDiagnostics(diag)
			return nil, extErr
		}
		builder.recordStage(StageWatchPageFetch, dur, http.StatusOK, true, nil)
	} else {
		t0 := time.Now()
		visitor, vErr := e.fetchVisitorData(ctx, id)
		vDur := time.Since(t0)
		if vErr != nil {
			builder.recordStage(StageWatchPageFetch, vDur, 0, false, vErr)
			extErr := classifyExtractionFailure(clientName, vErr)
			diag := builder.finishFailure(StageWatchPageFetch, vErr, extErr)
			extErr.Diagnostics = diag
			e.setLatestDiagnostics(diag)
			return nil, extErr
		}
		builder.diagnostics.SessionInputs.VisitorDataPresent = (visitor != "")
		builder.recordStage(StageWatchPageFetch, vDur, http.StatusOK, true, nil)

		t1 := time.Now()
		player, err = e.requestPlayer(
			ctx,
			id,
			profile,
			visitor,
		)
		playerReqDur = time.Since(t1)
		if err != nil {
			builder.recordStage(StagePlayerRequest, playerReqDur, 0, false, err)
			extErr := classifyExtractionFailure(clientName, err)
			diag := builder.finishFailure(StagePlayerRequest, err, extErr)
			extErr.Diagnostics = diag
			e.setLatestDiagnostics(diag)
			return nil, extErr
		}
	}

	if player.PlayabilityStatus.Status != "OK" {
		extErr := classifyPlayabilityError(
			clientName,
			player.PlayabilityStatus.Status,
			player.PlayabilityStatus.Reason,
		)
		builder.recordStage(StagePlayerRequest, playerReqDur, http.StatusOK, false, extErr)
		diag := builder.finishFailure(StagePlayerRequest, nil, extErr)
		extErr.Diagnostics = diag
		e.setLatestDiagnostics(diag)
		return nil, extErr
	}

	if clientName != ClientWeb {
		builder.recordStage(StagePlayerRequest, playerReqDur, http.StatusOK, true, nil)
	}

	if player.VideoDetails.IsLiveContent {
		extErr := &ExtractionError{
			Code:           ErrCodeNoSupportedFormats,
			Client:         clientName,
			PlaybackStatus: player.PlayabilityStatus.Status,
			Message:        "youtube: live-related videos are not supported by this prototype",
		}
		diag := builder.finishFailure(StageFormatDiscovery, nil, extErr)
		extErr.Diagnostics = diag
		e.setLatestDiagnostics(diag)
		return nil, extErr
	}

	rawURL := player.StreamingData.HLSManifestURL
	if rawURL == "" {
		extErr := &ExtractionError{
			Code:    ErrCodeManifestUnavailable,
			Client:  clientName,
			Message: fmt.Sprintf("youtube: %s player response did not expose an HLS manifest", clientName),
		}
		diag := builder.finishFailure(StageHLSManifestFetch, nil, extErr)
		extErr.Diagnostics = diag
		e.setLatestDiagnostics(diag)
		return nil, extErr
	}

	manifest, err := youtubeManifestResource(rawURL, profile)
	if err != nil {
		var extErr *ExtractionError
		if strings.Contains(err.Error(), "n parameter") {
			extErr = &ExtractionError{
				Code:    ErrCodeNChallengeReq,
				Client:  clientName,
				Message: fmt.Sprintf("youtube: %s HLS manifest requires an unsupported n-challenge", clientName),
				Err:     err,
			}
		} else {
			extErr = &ExtractionError{
				Code:    ErrCodeInvalidPlayerResponse,
				Client:  clientName,
				Message: fmt.Sprintf("youtube: %s invalid HLS manifest: %v", clientName, err),
				Err:     err,
			}
		}
		diag := builder.finishFailure(StageHLSManifestFetch, err, extErr)
		extErr.Diagnostics = diag
		e.setLatestDiagnostics(diag)
		return nil, extErr
	}

	allRaw := append([]playerFormat{}, player.StreamingData.Formats...)
	allRaw = append(allRaw, player.StreamingData.AdaptiveFormats...)
	diag := builder.finishSuccess(player, allRaw, 0)
	e.setLatestDiagnostics(diag)

	return &HLSExtraction{
		Media:            buildReport(id, player).Media,
		Manifest:         manifest,
		ExtractionReport: diag,
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
