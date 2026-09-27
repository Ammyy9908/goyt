package youtube

import (
	"context"
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
// detected by this implementation. A complete transfer still needs validation.
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

	for _, raw := range rawFormats {
		format, ok := convertDirectFormat(raw, profile)
		if ok {
			media.Formats = append(media.Formats, format)
		}
	}

	if len(media.Formats) == 0 {
		return nil, classifyNoSupportedFormats(clientName, player)
	}

	return media, nil
}

func convertDirectFormat(
	raw playerFormat,
	profile ClientProfile,
) (goyt.Format, bool) {
	if raw.URL == "" ||
		raw.SignatureCipher != "" ||
		raw.Cipher != "" ||
		len(raw.DRMFamilies) > 0 {
		return goyt.Format{}, false
	}

	u, err := url.Parse(raw.URL)
	if err != nil ||
		u.Hostname() == "" ||
		u.User != nil ||
		(u.Scheme != "https" && u.Scheme != "http") ||
		u.Query().Get("n") != "" {
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
			// Unknown codec composition cannot safely reach the planner.
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

	// Avoid silently treating a stream as video-only when its metadata says
	// it also contains audio but its codec list is incomplete.
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
			URL: raw.URL,
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
