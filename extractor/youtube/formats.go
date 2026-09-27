package youtube

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ammyy9908/goyt"
)

// ExtractDownloadable obtains candidate direct-download formats using the default client (visionos).
func (e *Extractor) ExtractDownloadable(
	ctx context.Context,
	u *url.URL,
) (*goyt.Media, error) {
	return e.ExtractDownloadableWithClient(ctx, u, DefaultClient)
}

// ExtractDownloadableWithClient obtains candidate direct-download formats for the specified client.
//
// "Downloadable" means the format has a supported direct URL and no challenge
// detected or an applicable challenge successfully transformed by a configured solver.
// A complete transfer still needs validation.
func (e *Extractor) ExtractDownloadableWithClient(
	ctx context.Context,
	u *url.URL,
	clientName string,
) (*goyt.Media, error) {
	clientName, err := ValidateClient(clientName)
	if err != nil {
		return nil, err
	}

	id, err := videoID(u)
	if err != nil {
		return nil, err
	}

	profile, _ := GetClientProfile(clientName)

	// Snapshot configured solver for the duration of this extraction.
	solver := e.Solver()

	var watchHTML []byte
	var player *playerResponse

	if clientName == ClientWeb {
		player, watchHTML, err = e.fetchWatchPagePlayerAndHTML(ctx, id)
		if err != nil {
			return nil, classifyExtractionFailure(clientName, err)
		}
	} else {
		visitor, vErr := e.fetchVisitorData(ctx, id)
		if vErr != nil {
			return nil, classifyExtractionFailure(clientName, vErr)
		}

		player, err = e.requestPlayer(ctx, id, profile, visitor)
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
			Message:        "youtube: this prototype does not support live-related videos",
		}
	}

	report := buildReport(id, player)
	media := report.Media

	rawFormats := append(
		[]playerFormat{},
		player.StreamingData.Formats...,
	)
	rawFormats = append(
		rawFormats,
		player.StreamingData.AdaptiveFormats...,
	)

	var unchallenged []candidateFormat
	var challenged []candidateFormat

	for _, raw := range rawFormats {
		cand, ok := parseCandidateFormat(raw)
		if !ok {
			continue
		}
		if cand.isChallenged {
			challenged = append(challenged, cand)
		} else {
			unchallenged = append(unchallenged, cand)
		}
	}

	// 1. Convert direct unchallenged candidates immediately.
	for _, cand := range unchallenged {
		if format, ok := convertResolvedFormat(cand.raw, cand.directURL, profile); ok {
			media.Formats = append(media.Formats, format)
		}
	}

	// 2. If challenged formats exist and a solver is configured, solve challenges.
	if len(challenged) > 0 && solver != nil {
		if len(watchHTML) == 0 {
			watchHTML, err = e.fetchWatchPageHTML(ctx, id)
			if err != nil {
				if len(media.Formats) == 0 {
					return nil, classifyExtractionFailure(clientName, err)
				}
				return media, nil
			}
		}

		scriptURL, sErr := DiscoverPlayerScriptURL(watchHTML)
		if sErr != nil {
			if len(media.Formats) == 0 {
				return nil, classifyExtractionFailure(clientName, sErr)
			}
			return media, nil
		}

		script, fErr := e.fetchPlayerScript(ctx, scriptURL)
		if fErr != nil {
			if len(media.Formats) == 0 {
				return nil, classifyExtractionFailure(clientName, fErr)
			}
			return media, nil
		}

		// Build deduplicated batch.
		batch := ChallengeBatch{}
		sigToID := make(map[string]string)
		nToID := make(map[string]string)
		expectedSigIDs := make(map[string]bool)
		expectedNIDs := make(map[string]bool)

		for _, cand := range challenged {
			if len(batch.Signatures)+len(batch.NParams) >= MaxChallengeBatchSize {
				break
			}
			if cand.hasSig {
				if _, exists := sigToID[cand.cipherS]; !exists {
					sigID := fmt.Sprintf("sig-%d", len(batch.Signatures))
					sigToID[cand.cipherS] = sigID
					expectedSigIDs[sigID] = true
					batch.Signatures = append(batch.Signatures, SignatureChallenge{
						ID:           sigID,
						CipherString: cand.cipherS,
						TargetParam:  cand.cipherSP,
					})
				}
			}
			if cand.hasN {
				if _, exists := nToID[cand.nVal]; !exists {
					nID := fmt.Sprintf("n-%d", len(batch.NParams))
					nToID[cand.nVal] = nID
					expectedNIDs[nID] = true
					batch.NParams = append(batch.NParams, NChallenge{
						ID:       nID,
						RawValue: cand.nVal,
					})
				}
			}
		}

		solverResult, solveErr := solver.SolveChallenges(ctx, script, batch)
		if solveErr != nil {
			if len(media.Formats) == 0 {
				return nil, classifyExtractionFailure(clientName, fmt.Errorf("challenge solver: %w", solveErr))
			}
			return media, nil
		}

		// Validate solver output against batch requests.
		validatedSigs := make(map[string]string)
		for id, res := range solverResult.Signatures {
			if !expectedSigIDs[id] || res.ID != id || res.Error != nil || res.Deciphered == "" || len(res.Deciphered) > MaxTransformedValueLength {
				continue
			}
			validatedSigs[id] = res.Deciphered
		}

		validatedNParams := make(map[string]string)
		for id, res := range solverResult.NParams {
			if !expectedNIDs[id] || res.ID != id || res.Error != nil || res.Transformed == "" || len(res.Transformed) > MaxTransformedValueLength {
				continue
			}
			validatedNParams[id] = res.Transformed
		}

		// Apply validated results to each challenged candidate.
		for _, cand := range challenged {
			var decipheredSig string
			var transformedN string
			failed := false

			if cand.hasSig {
				sigID := sigToID[cand.cipherS]
				deciphered, ok := validatedSigs[sigID]
				if !ok {
					failed = true
				} else {
					decipheredSig = deciphered
				}
			}

			if cand.hasN && !failed {
				nID := nToID[cand.nVal]
				transformed, ok := validatedNParams[nID]
				if !ok {
					failed = true
				} else {
					transformedN = transformed
				}
			}

			if failed {
				continue
			}

			targetRaw := cand.cipherURL
			if targetRaw == "" {
				targetRaw = cand.directURL
			}

			parsedURL, err := url.Parse(targetRaw)
			if err != nil {
				continue
			}

			q := parsedURL.Query()
			if cand.hasSig {
				q.Set(cand.cipherSP, decipheredSig)
			}
			if cand.hasN {
				q.Set("n", transformedN)
			}
			parsedURL.RawQuery = q.Encode()

			if format, ok := convertResolvedFormat(cand.raw, parsedURL.String(), profile); ok {
				media.Formats = append(media.Formats, format)
			}
		}
	}

	if len(media.Formats) == 0 {
		return nil, classifyNoSupportedFormats(clientName, player)
	}

	return media, nil
}

type candidateFormat struct {
	raw          playerFormat
	directURL    string
	cipherURL    string
	cipherS      string
	cipherSP     string
	hasSig       bool
	hasN         bool
	nVal         string
	isChallenged bool
}

func parseCandidateFormat(raw playerFormat) (candidateFormat, bool) {
	if len(raw.DRMFamilies) > 0 {
		return candidateFormat{}, false
	}

	// Reject conflicting simultaneous cipher formats.
	if raw.SignatureCipher != "" && raw.Cipher != "" {
		return candidateFormat{}, false
	}

	cipher := raw.SignatureCipher
	if cipher == "" {
		cipher = raw.Cipher
	}

	if cipher != "" {
		q, err := url.ParseQuery(cipher)
		if err != nil {
			return candidateFormat{}, false
		}

		// Reject ambiguous duplicate critical fields even when their values match.
		if len(q["url"]) > 1 || len(q["s"]) > 1 || len(q["sp"]) > 1 {
			return candidateFormat{}, false
		}

		rawURL := q.Get("url")
		s := q.Get("s")
		sp := q.Get("sp")
		if sp == "" {
			sp = "sig" // authoritative YouTube fallback parameter (yt-dlp youtube.py)
		}

		if rawURL == "" || s == "" {
			return candidateFormat{}, false
		}

		parsedMediaURL, err := url.Parse(rawURL)
		if err != nil || parsedMediaURL.Hostname() == "" || parsedMediaURL.User != nil ||
			(parsedMediaURL.Scheme != "https" && parsedMediaURL.Scheme != "http") {
			return candidateFormat{}, false
		}

		// Reject duplicate n parameters in the media URL query.
		if len(parsedMediaURL.Query()["n"]) > 1 {
			return candidateFormat{}, false
		}

		nVal := parsedMediaURL.Query().Get("n")
		hasN := nVal != ""

		return candidateFormat{
			raw:          raw,
			cipherURL:    rawURL,
			cipherS:      s,
			cipherSP:     sp,
			hasSig:       true,
			hasN:         hasN,
			nVal:         nVal,
			isChallenged: true,
		}, true
	}

	if raw.URL != "" {
		parsed, err := url.Parse(raw.URL)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
			(parsed.Scheme != "https" && parsed.Scheme != "http") {
			return candidateFormat{}, false
		}

		// Reject duplicate n parameters in the direct URL query.
		if len(parsed.Query()["n"]) > 1 {
			return candidateFormat{}, false
		}

		nVal := parsed.Query().Get("n")
		hasN := nVal != ""

		return candidateFormat{
			raw:          raw,
			directURL:    raw.URL,
			hasSig:       false,
			hasN:         hasN,
			nVal:         nVal,
			isChallenged: hasN,
		}, true
	}

	return candidateFormat{}, false
}

func convertDirectFormat(
	raw playerFormat,
	profile ClientProfile,
) (goyt.Format, bool) {
	cand, ok := parseCandidateFormat(raw)
	if !ok || cand.isChallenged {
		return goyt.Format{}, false
	}
	return convertResolvedFormat(raw, cand.directURL, profile)
}

func convertResolvedFormat(
	raw playerFormat,
	resolvedURL string,
	profile ClientProfile,
) (goyt.Format, bool) {
	if resolvedURL == "" || len(raw.DRMFamilies) > 0 {
		return goyt.Format{}, false
	}

	u, err := url.Parse(resolvedURL)
	if err != nil ||
		u.Hostname() == "" ||
		u.User != nil ||
		(u.Scheme != "https" && u.Scheme != "http") {
		return goyt.Format{}, false
	}

	mediaType, params, err := mime.ParseMediaType(raw.MIMEType)
	if err != nil {
		return goyt.Format{}, false
	}

	container := ""
	switch mediaType {
	case "video/mp4", "audio/mp4":
		container = "mp4"
	case "video/webm", "audio/webm":
		container = "webm"
	default:
		return goyt.Format{}, false
	}

	videoCodec := "none"
	audioCodec := "none"

	for _, codec := range strings.Split(params["codecs"], ",") {
		codec = strings.ToLower(strings.TrimSpace(codec))

		switch {
		case strings.HasPrefix(codec, "avc1"),
			strings.HasPrefix(codec, "avc3"):
			videoCodec = codec

		case codec == "vp8",
			codec == "vp9",
			strings.HasPrefix(codec, "vp08"),
			strings.HasPrefix(codec, "vp09"),
			strings.HasPrefix(codec, "av01"):
			videoCodec = codec

		case strings.HasPrefix(codec, "mp4a"),
			codec == "opus",
			codec == "vorbis":
			audioCodec = codec

		default:
			return goyt.Format{}, false
		}
	}

	if strings.HasPrefix(mediaType, "video/") && videoCodec == "none" {
		return goyt.Format{}, false
	}

	if strings.HasPrefix(mediaType, "audio/") &&
		(audioCodec == "none" || videoCodec != "none") {
		return goyt.Format{}, false
	}

	if audioCodec == "none" &&
		(raw.AudioQuality != "" || raw.AudioChannels > 0) {
		return goyt.Format{}, false
	}

	userAgent := profile.Context.UserAgent
	if userAgent == "" {
		userAgent = "Mozilla/5.0"
	}

	format := goyt.Format{
		ID:         strconv.Itoa(raw.Itag),
		Protocol:   goyt.ProtocolHTTP,
		Container:  container,
		VideoCodec: videoCodec,
		AudioCodec: audioCodec,
		Resource: goyt.Resource{
			URL: resolvedURL,
			Headers: http.Header{
				"User-Agent": []string{userAgent},
			},
		},
	}

	if raw.AudioTrack != nil {
		format.AudioTrackID = raw.AudioTrack.ID
		format.AudioTrackName = raw.AudioTrack.DisplayName
		format.AudioIsDefault = raw.AudioTrack.AudioIsDefault
		format.AudioIsOriginal = isOriginalAudioName(raw.AudioTrack.DisplayName) ||
			strings.Contains(strings.ToLower(raw.AudioTrack.ID), "original")
		tag, _, _ := strings.Cut(raw.AudioTrack.ID, ".")
		format.Language = strings.ToLower(strings.TrimSpace(tag))
	}

	if raw.Width > 0 {
		width := raw.Width
		format.Width = &width
	}

	if raw.Height > 0 {
		height := raw.Height
		format.Height = &height
	}

	if raw.Bitrate > 0 {
		bitrate := raw.Bitrate
		format.Bitrate = &bitrate
	}

	if size, err := strconv.ParseInt(
		raw.ContentLength,
		10,
		64,
	); err == nil && size >= 0 {
		format.SizeBytes = &size
	}

	if seconds, err := strconv.ParseInt(
		u.Query().Get("expire"),
		10,
		64,
	); err == nil && seconds > 0 {
		expires := time.Unix(seconds, 0)
		format.Resource.ExpiresAt = &expires
	}

	return format, true
}
