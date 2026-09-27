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

type HLSTrack struct {
	Playlist *HLSPlaylist
	BaseURL  *url.URL
	Headers  http.Header
}

type ResolvedHLS struct {
	Video           *HLSTrack
	Audio           *HLSTrack
	SelectedVariant *HLSVariant
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
		return nil, nil, nil, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
		}
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

// ResolvePlaylist preserves the existing default audio-selection behavior.
func (h *HLSDownloader) ResolvePlaylist(
	ctx context.Context,
	resource Resource,
	maxHeight int,
) (*ResolvedHLS, error) {
	return h.ResolvePlaylistLanguage(ctx, resource, maxHeight, "")
}

// ResolvePlaylistLanguage selects a supported variant and, when requested,
// an external audio rendition matching the declared language.
//
// An explicit language never falls back to an unknown or different language.
func (h *HLSDownloader) ResolvePlaylistLanguage(
	ctx context.Context,
	resource Resource,
	maxHeight int,
	language string,
) (*ResolvedHLS, error) {
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

		return &ResolvedHLS{
			Video: &HLSTrack{
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

	result := &ResolvedHLS{
		Video:           video,
		SelectedVariant: &selection.Variant,
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

// ResolveRefreshedMaster resolves a refreshed master playlist by matching the previously selected
// variant and audio rendition.
func (h *HLSDownloader) ResolveRefreshedMaster(
	ctx context.Context,
	resource Resource,
	originalVariant HLSVariant,
	originalAudio *HLSAudioRendition,
) (*ResolvedHLS, error) {
	data, base, headers, err := h.fetchPlaylist(ctx, resource)
	if err != nil {
		return nil, err
	}

	if !isHLSMaster(data) {
		return nil, errors.New("goyt: expected master playlist on refreshed HLS manifest")
	}

	master, err := ParseHLSMaster(data, base)
	if err != nil {
		return nil, err
	}

	newVariant, err := MatchRefreshedVariant(originalVariant, master)
	if err != nil {
		return nil, err
	}

	video, err := h.resolveMediaTrack(
		ctx,
		newVariant.URL,
		base,
		headers,
	)
	if err != nil {
		return nil, fmt.Errorf("goyt: video playlist: %w", err)
	}

	result := &ResolvedHLS{
		Video:           video,
		SelectedVariant: newVariant,
	}

	if originalAudio != nil {
		if newVariant.AudioGroup == "" {
			return nil, errors.New("goyt: refreshed variant does not specify an audio group")
		}

		newAudio, err := MatchRefreshedAudioRendition(*originalAudio, master, newVariant.AudioGroup)
		if err != nil {
			return nil, err
		}

		audio, err := h.resolveMediaTrack(
			ctx,
			newAudio.URL,
			base,
			headers,
		)
		if err != nil {
			return nil, fmt.Errorf("goyt: audio playlist: %w", err)
		}

		result.Audio = audio
		result.SelectedAudio = newAudio
		result.AudioIsOriginal = isOriginalAudioName(newAudio.Name)
	}

	return result, nil
}

// ResolveAudioLanguage resolves a master or media playlist for audio-only downloads.
func (h *HLSDownloader) ResolveAudioLanguage(
	ctx context.Context,
	resource Resource,
	audioLanguage string,
) (*HLSTrack, *HLSAudioRendition, bool, string, error) {
	data, base, headers, err := h.fetchPlaylist(ctx, resource)
	if err != nil {
		return nil, nil, false, "", err
	}

	if !isHLSMaster(data) {
		if audioLanguage != "" {
			return nil, nil, false, "", errors.New(
				"goyt: cannot select an audio language from a media playlist without master rendition metadata",
			)
		}

		playlist, err := ParseHLS(data, base)
		if err != nil {
			return nil, nil, false, "", err
		}

		return &HLSTrack{
			Playlist: playlist,
			BaseURL:  base,
			Headers:  headers,
		}, nil, false, "", nil
	}

	master, err := ParseHLSMaster(data, base)
	if err != nil {
		return nil, nil, false, "", err
	}

	audioSelection, err := master.SelectAudioOnly(audioLanguage)
	if err != nil {
		return nil, nil, false, "", err
	}

	track, err := h.resolveMediaTrack(ctx, audioSelection.Audio.URL, base, headers)
	if err != nil {
		return nil, nil, false, "", err
	}

	return track, &audioSelection.Audio, audioSelection.AudioIsOriginal, audioSelection.AudioWarning, nil
}

// ResolveRefreshedAudioOnlyMaster resolves a refreshed master playlist in audio-only mode by matching the
// previously selected audio rendition.
func (h *HLSDownloader) ResolveRefreshedAudioOnlyMaster(
	ctx context.Context,
	resource Resource,
	originalAudio HLSAudioRendition,
) (*HLSTrack, *HLSAudioRendition, error) {
	data, base, headers, err := h.fetchPlaylist(ctx, resource)
	if err != nil {
		return nil, nil, err
	}

	if !isHLSMaster(data) {
		return nil, nil, errors.New("goyt: expected master playlist on refreshed HLS manifest")
	}

	master, err := ParseHLSMaster(data, base)
	if err != nil {
		return nil, nil, err
	}

	newAudio, err := MatchRefreshedAudioRendition(originalAudio, master, "")
	if err != nil {
		return nil, nil, err
	}

	track, err := h.resolveMediaTrack(
		ctx,
		newAudio.URL,
		base,
		headers,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("goyt: audio playlist: %w", err)
	}

	return track, newAudio, nil
}

// ResolveMediaPlaylist fetches and parses a single media playlist.
func (h *HLSDownloader) ResolveMediaPlaylist(
	ctx context.Context,
	resource Resource,
) (*HLSTrack, error) {
	data, base, headers, err := h.fetchPlaylist(ctx, resource)
	if err != nil {
		return nil, err
	}
	if isHLSMaster(data) {
		return nil, errors.New("goyt: expected media playlist, got master playlist")
	}
	playlist, err := ParseHLS(data, base)
	if err != nil {
		return nil, err
	}
	return &HLSTrack{
		Playlist: playlist,
		BaseURL:  base,
		Headers:  headers,
	}, nil
}

func (h *HLSDownloader) resolveMediaTrack(
	ctx context.Context,
	rawURL string,
	parent *url.URL,
	parentHeaders http.Header,
) (*HLSTrack, error) {
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

	return &HLSTrack{
		Playlist: playlist,
		BaseURL:  finalURL,
		Headers:  finalHeaders,
	}, nil
}
