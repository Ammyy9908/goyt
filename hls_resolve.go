package goyt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type hlsTrack struct {
	Playlist *HLSPlaylist
	BaseURL  *url.URL
	Headers  http.Header
}

type resolvedHLS struct {
	Video           *hlsTrack
	Audio           *hlsTrack
	SelectedAudio   *HLSAudioRendition
	AudioIsOriginal bool
	AudioWarning    string
}

func (h *HLSDownloader) fetchPlaylist(
	ctx context.Context,
	resource Resource,
) ([]byte, *url.URL, http.Header, error) {
	u, err := url.Parse(resource.URL)
	if err != nil || !validHLSURL(u) {
		return nil, nil, nil, errors.New("goyt: invalid playlist URL")
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		u.String(),
		nil,
	)
	if err != nil {
		return nil, nil, nil, err
	}

	req.Header = resource.Headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Del("Range")
	req.Header.Del("If-Range")

	resp, err := h.http.client.Do(req)
	if err != nil {
		return nil, nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, nil, fmt.Errorf(
			"goyt: playlist returned HTTP %d",
			resp.StatusCode,
		)
	}

	data, err := io.ReadAll(
		io.LimitReader(resp.Body, maxHLSPlaylistBytes+1),
	)
	if err != nil {
		return nil, nil, nil, err
	}

	if len(data) > maxHLSPlaylistBytes {
		return nil, nil, nil, errors.New(
			"goyt: playlist exceeds size limit",
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}

	return data,
		resp.Request.URL,
		resp.Request.Header.Clone(),
		nil
}

// resolvePlaylist preserves the existing default audio-selection behavior.
func (h *HLSDownloader) resolvePlaylist(
	ctx context.Context,
	resource Resource,
	maxHeight int,
) (*resolvedHLS, error) {
	return h.resolvePlaylistLanguage(ctx, resource, maxHeight, "")
}

// resolvePlaylistLanguage selects a supported variant and, when requested,
// an external audio rendition matching the declared language.
//
// An explicit language never falls back to an unknown or different language.
func (h *HLSDownloader) resolvePlaylistLanguage(
	ctx context.Context,
	resource Resource,
	maxHeight int,
	language string,
) (*resolvedHLS, error) {
	language = strings.TrimSpace(language)

	data, base, headers, err := h.fetchPlaylist(ctx, resource)
	if err != nil {
		return nil, err
	}

	if !isHLSMaster(data) {
		if language != "" {
			return nil, errors.New(
				"goyt: cannot select an audio language from a media playlist " +
					"without master rendition metadata",
			)
		}

		playlist, err := ParseHLS(data, base)
		if err != nil {
			return nil, err
		}

		return &resolvedHLS{
			Video: &hlsTrack{
				Playlist: playlist,
				BaseURL:  base,
				Headers:  headers,
			},
		}, nil
	}

	master, err := ParseHLSMaster(data, base)
	if err != nil {
		return nil, err
	}

	selection, err := master.SelectWithAudioLanguage(
		maxHeight,
		language,
	)
	if err != nil {
		return nil, err
	}

	video, err := h.resolveMediaTrack(
		ctx,
		selection.Variant.URL,
		base,
		headers,
	)
	if err != nil {
		return nil, fmt.Errorf("goyt: video playlist: %w", err)
	}

	result := &resolvedHLS{
		Video:           video,
		SelectedAudio:   selection.Audio,
		AudioIsOriginal: selection.AudioIsOriginal,
		AudioWarning:    selection.AudioWarning,
	}

	if selection.Audio != nil {
		audio, err := h.resolveMediaTrack(
			ctx,
			selection.Audio.URL,
			base,
			headers,
		)
		if err != nil {
			return nil, fmt.Errorf("goyt: audio playlist: %w", err)
		}

		result.Audio = audio
	}

	return result, nil
}

func (h *HLSDownloader) resolveMediaTrack(
	ctx context.Context,
	rawURL string,
	parent *url.URL,
	parentHeaders http.Header,
) (*hlsTrack, error) {
	child, err := url.Parse(rawURL)
	if err != nil || !validHLSURL(child) {
		return nil, errors.New("goyt: invalid child playlist URL")
	}

	headers := parentHeaders.Clone()
	if origin(parent) != origin(child) {
		headers = nil
	}

	data, finalURL, finalHeaders, err := h.fetchPlaylist(
		ctx,
		Resource{
			URL:     rawURL,
			Headers: headers,
		},
	)
	if err != nil {
		return nil, err
	}

	if isHLSMaster(data) {
		return nil, errors.New(
			"goyt: nested HLS masters are unsupported",
		)
	}

	playlist, err := ParseHLS(data, finalURL)
	if err != nil {
		return nil, err
	}

	return &hlsTrack{
		Playlist: playlist,
		BaseURL:  finalURL,
		Headers:  finalHeaders,
	}, nil
}
