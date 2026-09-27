package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ammyy9908/goyt"
)

var ErrPlayerResponseMissing = errors.New(
	"youtube: embedded player response not found; " +
		"the page may require consent, authentication, or a different extraction strategy",
)

var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// Match common assignments without attempting to parse JSON using regex.
// encoding/json handles the object itself, including nested braces.
var playerAssignment = regexp.MustCompile(
	`(?:\bytInitialPlayerResponse\s*=|window\s*\[\s*"ytInitialPlayerResponse"\s*\]\s*=)\s*`,
)

const maxPageBytes = 12 << 20

type Extractor struct {
	client *http.Client
}

var _ goyt.Extractor = (*Extractor)(nil)

func New(client *http.Client) *Extractor {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	return &Extractor{client: client}
}

func (e *Extractor) Name() string {
	return "youtube"
}

func (e *Extractor) Match(u *url.URL) bool {
	_, err := videoID(u)
	return err == nil
}

// Extract currently returns metadata only.
//
// Media.Formats intentionally remains empty: format discovery alone does
// not establish that the playback URLs are usable by our downloader.
// Use Inspect to see the discovered format inventory and limitations.
func (e *Extractor) Extract(
	ctx context.Context,
	u *url.URL,
) (*goyt.Media, error) {
	report, err := e.Inspect(ctx, u)
	if err != nil {
		return nil, err
	}

	return report.Media, nil
}

// DiscoveredFormat is diagnostic information, not a download-ready Format.
// Signed URLs and cipher contents are deliberately not exported.
type DiscoveredFormat struct {
	ID                 int
	MIMEType           string
	Codecs             string
	Quality            string
	Width              int
	Height             int
	Bitrate            int64
	HasDirectURL       bool
	SignatureChallenge bool
	NChallenge         bool
	DRMReported        bool
}

type DiscoveredAudioTrack struct {
	ID             string
	DisplayName    string
	AudioIsDefault bool
	IsOriginal     bool
}

type DiscoveredHLSRendition struct {
	GroupID    string
	Name       string
	Language   string
	Default    bool
	AutoSelect bool
	IsOriginal bool
}

type Report struct {
	Media           *goyt.Media
	PlaybackStatus  string
	PlaybackReason  string
	Formats         []DiscoveredFormat
	AudioTracks     []DiscoveredAudioTrack
	HLSRenditions   []DiscoveredHLSRendition
	HasHLSManifest  bool
	HasDASHManifest bool
	Limitations     []string
	HasSABREndpoint bool
}

type playerResponse struct {
	VideoDetails struct {
		VideoID       string `json:"videoId"`
		Title         string `json:"title"`
		LengthSeconds string `json:"lengthSeconds"`
		IsLiveContent bool   `json:"isLiveContent"`
	} `json:"videoDetails"`

	PlayabilityStatus struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"playabilityStatus"`

	StreamingData struct {
		Formats               []playerFormat `json:"formats"`
		AdaptiveFormats       []playerFormat `json:"adaptiveFormats"`
		HLSManifestURL        string         `json:"hlsManifestUrl"`
		DASHManifestURL       string         `json:"dashManifestUrl"`
		ServerABRStreamingURL string         `json:"serverAbrStreamingUrl"`
	} `json:"streamingData"`

	Captions struct {
		PlayerCaptionsTracklistRenderer struct {
			AudioTracks []struct {
				CaptionTrackIndices []int  `json:"captionTrackIndices"`
				DefaultTrackIndex   int    `json:"defaultCaptionTrackIndex"`
				HasDefaultTrack     bool   `json:"hasDefaultTrack"`
				AudioIsDefault      bool   `json:"audioIsDefault"`
				ID                  string `json:"id"`
				DisplayName         string `json:"displayName"`
				Visibility          string `json:"visibility"`
			} `json:"audioTracks"`
			DefaultAudioTrackIndex int `json:"defaultAudioTrackIndex"`
		} `json:"playerCaptionsTracklistRenderer"`
	} `json:"captions"`
}

type playerAudioTrack struct {
	DisplayName    string `json:"displayName"`
	ID             string `json:"id"`
	AudioIsDefault bool   `json:"audioIsDefault"`
}

type playerFormat struct {
	Itag            int               `json:"itag"`
	MIMEType        string            `json:"mimeType"`
	QualityLabel    string            `json:"qualityLabel"`
	Height          int               `json:"height"`
	Bitrate         int64             `json:"bitrate"`
	URL             string            `json:"url"`
	SignatureCipher string            `json:"signatureCipher"`
	Cipher          string            `json:"cipher"`
	DRMFamilies     []string          `json:"drmFamilies"`
	Width           int               `json:"width"`
	ContentLength   string            `json:"contentLength"`
	AudioQuality    string            `json:"audioQuality"`
	AudioChannels   int               `json:"audioChannels"`
	AudioTrack      *playerAudioTrack `json:"audioTrack"`
}

func videoID(u *url.URL) (string, error) {
	if u == nil ||
		(u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil {
		return "", errors.New("youtube: invalid URL")
	}

	var id string

	switch strings.ToLower(u.Hostname()) {
	case "youtu.be":
		id = strings.Trim(u.Path, "/")

	case "youtube.com", "www.youtube.com", "m.youtube.com":
		if u.Path == "/watch" {
			id = u.Query().Get("v")
		} else {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) == 2 {
				switch parts[0] {
				case "shorts", "embed", "live":
					id = parts[1]
				}
			}
		}

	case "music.youtube.com":
		if u.Path == "/watch" {
			id = u.Query().Get("v")
		}

	default:
		return "", errors.New("youtube: unsupported host")
	}

	if !videoIDPattern.MatchString(id) {
		return "", errors.New("youtube: invalid or unsupported video URL")
	}

	return id, nil
}

func (e *Extractor) Inspect(
	ctx context.Context,
	u *url.URL,
) (*Report, error) {
	id, err := videoID(u)
	if err != nil {
		return nil, err
	}

	watchURL := "https://www.youtube.com/watch?v=" + id + "&hl=en"

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		watchURL,
		nil,
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube: fetch watch page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"youtube: watch page returned HTTP %d",
			resp.StatusCode,
		)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if err != nil {
		return nil, err
	}

	if len(body) > maxPageBytes {
		return nil, errors.New("youtube: watch page exceeds size limit")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	player, err := parsePlayer(body)
	if err != nil {
		return nil, err
	}

	if player.VideoDetails.VideoID != "" &&
		player.VideoDetails.VideoID != id {
		return nil, errors.New("youtube: response video ID does not match request")
	}

	return buildReport(id, player), nil
}

func parsePlayer(page []byte) (*playerResponse, error) {
	for _, match := range playerAssignment.FindAllIndex(page, -1) {
		decoder := json.NewDecoder(bytes.NewReader(page[match[1]:]))

		var player playerResponse
		if err := decoder.Decode(&player); err != nil {
			continue
		}

		if player.VideoDetails.VideoID != "" ||
			player.PlayabilityStatus.Status != "" {
			return &player, nil
		}
	}

	return nil, ErrPlayerResponseMissing
}

func buildReport(id string, player *playerResponse) *Report {
	media := &goyt.Media{
		ID:        id,
		SourceURL: "https://www.youtube.com/watch?v=" + id,
		Title:     player.VideoDetails.Title,
	}

	// ParseDuration also checks overflow.
	if seconds, err := strconv.ParseInt(
		player.VideoDetails.LengthSeconds,
		10,
		64,
	); err == nil && seconds >= 0 {
		if duration, err := time.ParseDuration(
			strconv.FormatInt(seconds, 10) + "s",
		); err == nil {
			media.Duration = &duration
		}
	}

	report := &Report{
		Media:           media,
		PlaybackStatus:  player.PlayabilityStatus.Status,
		PlaybackReason:  player.PlayabilityStatus.Reason,
		HasHLSManifest:  player.StreamingData.HLSManifestURL != "",
		HasDASHManifest: player.StreamingData.DASHManifestURL != "",
		HasSABREndpoint: player.StreamingData.ServerABRStreamingURL != "",
		Limitations: []string{
			"Page-response inspection only; format inventory may be incomplete.",
			"No JavaScript challenge solver or PO-token provider is implemented.",
			"Discovered URLs have not been verified for playback or downloading.",
		},
	}

	formats := append(
		[]playerFormat{},
		player.StreamingData.Formats...,
	)
	formats = append(formats, player.StreamingData.AdaptiveFormats...)

	for _, f := range formats {
		cipher := f.SignatureCipher
		if cipher == "" {
			cipher = f.Cipher
		}

		cipherValues, _ := url.ParseQuery(cipher)

		candidateURL := f.URL
		if candidateURL == "" {
			candidateURL = cipherValues.Get("url")
		}

		hasN := false
		if parsed, err := url.Parse(candidateURL); err == nil {
			hasN = parsed.Query().Get("n") != ""
		}

		mediaType := f.MIMEType
		codecs := ""
		if mt, params, err := mime.ParseMediaType(f.MIMEType); err == nil {
			mediaType = mt
			codecs = params["codecs"]
		}

		report.Formats = append(report.Formats, DiscoveredFormat{
			ID:                 f.Itag,
			MIMEType:           mediaType,
			Codecs:             codecs,
			Quality:            f.QualityLabel,
			Width:              f.Width,
			Height:             f.Height,
			Bitrate:            f.Bitrate,
			HasDirectURL:       f.URL != "",
			SignatureChallenge: cipherValues.Get("s") != "",
			NChallenge:         hasN,
			DRMReported:        len(f.DRMFamilies) > 0,
		})
	}

	seenAudio := make(map[string]bool)
	for _, at := range player.Captions.PlayerCaptionsTracklistRenderer.AudioTracks {
		if at.ID != "" && !seenAudio[at.ID] {
			seenAudio[at.ID] = true
			isOrig := isOriginalAudioName(at.DisplayName) || strings.Contains(strings.ToLower(at.ID), "original")
			report.AudioTracks = append(report.AudioTracks, DiscoveredAudioTrack{
				ID:             at.ID,
				DisplayName:    at.DisplayName,
				AudioIsDefault: at.AudioIsDefault,
				IsOriginal:     isOrig,
			})
		}
	}

	for _, f := range player.StreamingData.AdaptiveFormats {
		if f.AudioTrack != nil && f.AudioTrack.ID != "" && !seenAudio[f.AudioTrack.ID] {
			seenAudio[f.AudioTrack.ID] = true
			isOrig := isOriginalAudioName(f.AudioTrack.DisplayName) || strings.Contains(strings.ToLower(f.AudioTrack.ID), "original")
			report.AudioTracks = append(report.AudioTracks, DiscoveredAudioTrack{
				ID:             f.AudioTrack.ID,
				DisplayName:    f.AudioTrack.DisplayName,
				AudioIsDefault: f.AudioTrack.AudioIsDefault,
				IsOriginal:     isOrig,
			})
		}
	}

	if len(report.Formats) == 0 {
		report.Limitations = append(
			report.Limitations,
			"No conventional formats were exposed in this player response.",
		)
	}

	if player.VideoDetails.IsLiveContent {
		report.Limitations = append(
			report.Limitations,
			"This video is marked as live-related; live handling is not implemented.",
		)
	}

	if report.HasSABREndpoint {
		report.Limitations = append(
			report.Limitations,
			"A SABR streaming endpoint is present; goyt does not implement SABR playback.",
		)
	}

	return report
}

func isOriginalAudioName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "(original)") ||
		strings.Contains(lower, "[original]") ||
		strings.HasSuffix(lower, "original") ||
		strings.HasPrefix(lower, "original")
}
