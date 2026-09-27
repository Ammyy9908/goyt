package goyt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type HLSProgress struct {
	CompletedSegments int
	TotalSegments     int
}

type HLSResult struct {
	ExecutionResult
	PlaylistDuration time.Duration
	SelectedAudio    *HLSAudioRendition
	AudioIsOriginal  bool
	AudioWarning     string
}

type HLSAudioResult struct {
	ExecutionResult
	PlaylistDuration time.Duration
	SelectedAudio    *HLSAudioRendition
	AudioIsOriginal  bool
	AudioWarning     string
	OutputSpec       AudioOutputSpec
}

type HLSDownloader struct {
	http      *Downloader
	processor *FFmpeg
}

func NewHLSDownloader(
	client *http.Client,
	processor *FFmpeg,
) (*HLSDownloader, error) {
	if processor == nil {
		return nil, errors.New("goyt: HLS requires an FFmpeg processor")
	}

	return &HLSDownloader{
		http:      NewDownloader(client),
		processor: processor,
	}, nil
}

func (h *HLSDownloader) Download(
	ctx context.Context,
	resource Resource,
	destination string,
	maxHeight int,
	options DownloadOptions,
	progress func(HLSProgress),
) (*HLSResult, error) {
	return h.DownloadWithAudioLanguage(
		ctx,
		resource,
		destination,
		maxHeight,
		"",
		options,
		progress,
	)
}

// Download supports completed MPEG-TS media playlists and supported masters.
// Separate audio renditions are downloaded independently and merged.
//
// Intermediate files remain after failure. Whole-job resume is not implemented.
// maxHeight applies only to master-playlist variant selection.
func (h *HLSDownloader) DownloadWithAudioLanguage(
	ctx context.Context,
	resource Resource,
	destination string,
	maxHeight int,
	audioLanguage string,
	options DownloadOptions,
	progress func(HLSProgress),
) (*HLSResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if h == nil || h.http == nil || h.processor == nil {
		return nil, errors.New("goyt: create HLSDownloader with its constructor")
	}

	if destination == "" || maxHeight <= 0 {
		return nil, errors.New("goyt: destination and positive height are required")
	}

	if options.MaxRetries < 0 || options.RetryDelay < 0 || options.StallTimeout < 0 {
		return nil, errors.New("goyt: invalid retry options")
	}

	if _, err := outputMuxer(destination); err != nil {
		return nil, err
	}

	resolved, err := h.ResolvePlaylistLanguage(
		ctx,
		resource,
		maxHeight,
		audioLanguage,
	)
	if err != nil {
		return nil, err
	}

	return h.DownloadResolved(ctx, resolved, destination, options, progress)
}

func (h *HLSDownloader) DownloadResolved(
	ctx context.Context,
	resolved *ResolvedHLS,
	destination string,
	options DownloadOptions,
	progress func(HLSProgress),
) (*HLSResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if h == nil || h.http == nil || h.processor == nil {
		return nil, errors.New("goyt: create HLSDownloader with its constructor")
	}

	if resolved == nil || resolved.Video == nil {
		return nil, errors.New("goyt: invalid resolved HLS presentation")
	}

	tracks := []*HLSTrack{resolved.Video}
	if resolved.Audio != nil {
		// Reject clearly mismatched completed presentations before downloading.
		// This checks duration only; synchronization is validated by playback.
		if err := compareDuration(
			resolved.Audio.Playlist.Duration.Seconds(),
			resolved.Video.Playlist.Duration,
		); err != nil {
			return nil, fmt.Errorf("goyt: audio/video playlist mismatch: %w", err)
		}

		tracks = append(tracks, resolved.Audio)
	}

	totalSegments := 0
	for _, track := range tracks {
		totalSegments += len(track.Playlist.Segments)
	}

	output, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}

	workDir, err := os.MkdirTemp(filepath.Dir(output), ".goyt-hls-*")
	if err != nil {
		return nil, err
	}

	fail := func(stage string, err error) (*HLSResult, error) {
		return nil, &ExecutionError{
			Stage:   stage,
			WorkDir: workDir,
			Err:     err,
		}
	}

	completed := 0
	inputs := make([]string, len(tracks))

	for i, track := range tracks {
		inputs[i] = filepath.Join(workDir, fmt.Sprintf("track-%d.m3u8", i))

		err := h.downloadTrack(
			ctx,
			track,
			inputs[i],
			options,
			func() {
				completed++
				if progress != nil {
					progress(HLSProgress{
						CompletedSegments: completed,
						TotalSegments:     totalSegments,
					})
				}
			},
		)
		if err != nil {
			return fail(fmt.Sprintf("download track %d", i+1), err)
		}
	}

	staged := filepath.Join(workDir, "output"+filepath.Ext(output))

	localInput := inputs[0]

	if len(inputs) == 2 {
		localInput = filepath.Join(workDir, "master.m3u8")

		// All names below are generated locally, never copied from remote URLs.
		master := "#EXTM3U\n" +
			"#EXT-X-VERSION:3\n" +
			"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\"," +
			"NAME=\"Audio\",DEFAULT=YES,AUTOSELECT=YES," +
			"URI=\"track-1.m3u8\"\n" +
			"#EXT-X-STREAM-INF:BANDWIDTH=1000000,AUDIO=\"audio\"\n" +
			"track-0.m3u8\n"

		if err := os.WriteFile(localInput, []byte(master), 0600); err != nil {
			return fail("write local HLS master", err)
		}
	}

	// One HLS input lets FFmpeg interpret both tracks on their shared timeline.
	err = h.processor.process(
		ctx,
		"local-hls",
		[]string{localInput},
		[]string{"-map", "0:v:0", "-map", "0:a:0"},
		staged,
	)

	if err != nil {
		return fail("process HLS", err)
	}

	if err := ctx.Err(); err != nil {
		return fail("commit HLS", err)
	}

	info, err := os.Stat(staged)
	if err != nil {
		return fail("inspect HLS output", err)
	}

	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fail("inspect HLS output", errors.New("empty or invalid output"))
	}

	if err := os.Rename(staged, output); err != nil {
		return fail("commit HLS", err)
	}

	result := &HLSResult{
		ExecutionResult: ExecutionResult{
			Path:      output,
			SizeBytes: info.Size(),
		},
		PlaylistDuration: resolved.Video.Playlist.Duration,
		SelectedAudio:    resolved.SelectedAudio,
		AudioIsOriginal:  resolved.AudioIsOriginal,
		AudioWarning:     resolved.AudioWarning,
	}

	if err := os.RemoveAll(workDir); err != nil {
		result.WorkDir = workDir
		result.CleanupError = err
	}

	return result, nil
}

// DownloadAudio downloads an audio-only HLS presentation and converts it according to spec.
//
// If resource is a master playlist, it selects a supported external audio rendition.
// If resource is a media playlist, it is downloaded directly (confirmed to be audio-only).
// Zero video playlists or video segments are requested.
func (h *HLSDownloader) DownloadAudio(
	ctx context.Context,
	resource Resource,
	destination string,
	audioLanguage string,
	spec AudioOutputSpec,
	options DownloadOptions,
	progress func(HLSProgress),
) (*HLSAudioResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if h == nil || h.http == nil || h.processor == nil {
		return nil, errors.New("goyt: create HLSDownloader with its constructor")
	}

	if destination == "" {
		return nil, errors.New("goyt: destination is required")
	}

	var qualPtr *int
	if spec.Quality >= 0 {
		qual := spec.Quality
		qualPtr = &qual
	}

	resolvedSpec, err := ResolveAudioOutputSpec(
		spec.RequestedFormat,
		"aac", // HLS audio tracks are AAC
		qualPtr,
		spec.Bitrate,
	)
	if err != nil {
		return nil, err
	}

	if !strings.EqualFold(filepath.Ext(destination), resolvedSpec.Extension) {
		return nil, fmt.Errorf("goyt: destination extension must be %s", resolvedSpec.Extension)
	}

	if !resolvedSpec.Copy && resolvedSpec.Encoder != "" {
		has, err := h.processor.HasEncoder(ctx, resolvedSpec.Encoder)
		if err == nil && !has {
			return nil, fmt.Errorf("goyt: required FFmpeg audio encoder %q is not available", resolvedSpec.Encoder)
		}
	}

	if options.MaxRetries < 0 || options.RetryDelay < 0 || options.StallTimeout < 0 {
		return nil, errors.New("goyt: invalid retry options")
	}

	data, base, headers, err := h.fetchPlaylist(ctx, resource)
	if err != nil {
		return nil, err
	}

	var track *HLSTrack
	var selectedAudio *HLSAudioRendition
	var isOriginal bool
	var warning string

	if !isHLSMaster(data) {
		if audioLanguage != "" {
			return nil, errors.New(
				"goyt: cannot select an audio language from a media playlist without master rendition metadata",
			)
		}

		playlist, err := ParseHLS(data, base)
		if err != nil {
			return nil, err
		}

		track = &HLSTrack{
			Playlist: playlist,
			BaseURL:  base,
			Headers:  headers,
		}
	} else {
		master, err := ParseHLSMaster(data, base)
		if err != nil {
			return nil, err
		}

		audioSelection, err := master.SelectAudioOnly(audioLanguage)
		if err != nil {
			return nil, err
		}

		selectedAudio = &audioSelection.Audio
		isOriginal = audioSelection.AudioIsOriginal
		warning = audioSelection.AudioWarning

		track, err = h.resolveMediaTrack(ctx, audioSelection.Audio.URL, base, headers)
		if err != nil {
			return nil, err
		}
	}

	return h.DownloadAudioTrack(
		ctx,
		track,
		selectedAudio,
		isOriginal,
		warning,
		resolvedSpec,
		destination,
		options,
		progress,
	)
}

func (h *HLSDownloader) DownloadAudioTrack(
	ctx context.Context,
	track *HLSTrack,
	selectedAudio *HLSAudioRendition,
	isOriginal bool,
	warning string,
	resolvedSpec AudioOutputSpec,
	destination string,
	options DownloadOptions,
	progress func(HLSProgress),
) (*HLSAudioResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if h == nil || h.http == nil || h.processor == nil {
		return nil, errors.New("goyt: create HLSDownloader with its constructor")
	}

	if track == nil || track.Playlist == nil {
		return nil, errors.New("goyt: invalid audio track")
	}

	output, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}

	if info, err := os.Stat(output); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("goyt: destination must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	workDir, err := os.MkdirTemp(filepath.Dir(output), ".goyt-hls-*")
	if err != nil {
		return nil, err
	}

	fail := func(stage string, err error) (*HLSAudioResult, error) {
		return nil, &ExecutionError{
			Stage:   stage,
			WorkDir: workDir,
			Err:     err,
		}
	}

	localPlaylist := filepath.Join(workDir, "audio.m3u8")
	completed := 0
	totalSegments := len(track.Playlist.Segments)

	err = h.downloadTrack(
		ctx,
		track,
		localPlaylist,
		options,
		func() {
			completed++
			if progress != nil {
				progress(HLSProgress{
					CompletedSegments: completed,
					TotalSegments:     totalSegments,
				})
			}
		},
	)
	if err != nil {
		return fail("download audio segments", err)
	}

	staged := filepath.Join(workDir, "output"+resolvedSpec.Extension)

	err = h.processor.ConvertAudio(
		ctx,
		localPlaylist,
		staged,
		resolvedSpec,
		true,
	)
	if err != nil {
		return fail("process HLS audio", err)
	}

	if err := ctx.Err(); err != nil {
		return fail("commit HLS audio", err)
	}

	info, err := os.Stat(staged)
	if err != nil {
		return fail("inspect HLS audio output", err)
	}

	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fail("inspect HLS audio output", errors.New("empty or invalid output"))
	}

	if err := os.Rename(staged, output); err != nil {
		return fail("commit HLS audio", err)
	}

	result := &HLSAudioResult{
		ExecutionResult: ExecutionResult{
			Path:      output,
			SizeBytes: info.Size(),
		},
		PlaylistDuration: track.Playlist.Duration,
		SelectedAudio:    selectedAudio,
		AudioIsOriginal:  isOriginal,
		AudioWarning:     warning,
		OutputSpec:       resolvedSpec,
	}

	if err := os.RemoveAll(workDir); err != nil {
		result.WorkDir = workDir
		result.CleanupError = err
	}

	return result, nil
}

func (h *HLSDownloader) downloadTrack(
	ctx context.Context,
	track *HLSTrack,
	destination string,
	options DownloadOptions,
	onSegment func(),
) error {
	if track == nil || track.Playlist == nil || track.BaseURL == nil {
		return errors.New("goyt: invalid HLS track")
	}

	if len(track.Playlist.Segments) == 0 {
		return errors.New("goyt: empty HLS track")
	}

	targetDuration := 1
	for _, segment := range track.Playlist.Segments {
		if segment.Duration <= 0 {
			return errors.New("goyt: invalid HLS segment duration")
		}

		seconds := int(math.Ceil(segment.Duration.Seconds()))
		if seconds > targetDuration {
			targetDuration = seconds
		}
	}

	var playlist strings.Builder
	fmt.Fprintf(
		&playlist,
		"#EXTM3U\n"+
			"#EXT-X-VERSION:3\n"+
			"#EXT-X-TARGETDURATION:%d\n"+
			"#EXT-X-MEDIA-SEQUENCE:0\n"+
			"#EXT-X-PLAYLIST-TYPE:VOD\n",
		targetDuration,
	)

	// The destination is generated by Download: track-0.m3u8, etc.
	prefix := strings.TrimSuffix(
		filepath.Base(destination),
		filepath.Ext(destination),
	)

	trackExtension := ""

	for i, segment := range track.Playlist.Segments {
		if err := ctx.Err(); err != nil {
			return err
		}

		segmentURL, err := url.Parse(segment.URL)
		if err != nil || !validHLSURL(segmentURL) {
			return fmt.Errorf("goyt: segment %d has an invalid URL", i+1)
		}

		headers := track.Headers.Clone()
		if origin(track.BaseURL) != origin(segmentURL) {
			headers = nil
		}

		name := fmt.Sprintf("%s-segment-%05d", prefix, i)
		downloadPath := filepath.Join(
			filepath.Dir(destination),
			name+".download",
		)

		_, err = h.http.Download(
			ctx,
			Resource{URL: segment.URL, Headers: headers},
			downloadPath,
			options,
		)
		if err != nil {
			return fmt.Errorf("segment %d: %w", i+1, err)
		}

		extension, err := inspectHLSSegment(ctx, downloadPath)
		if err != nil {
			return fmt.Errorf("segment %d: %w", i+1, err)
		}

		if trackExtension != "" && extension != trackExtension {
			return fmt.Errorf(
				"goyt: segment %d changes container within a track",
				i+1,
			)
		}
		trackExtension = extension

		localName := name + extension
		localPath := filepath.Join(filepath.Dir(destination), localName)

		if err := os.Rename(downloadPath, localPath); err != nil {
			return err
		}

		// Only generated relative filenames appear in the local playlist.
		// Original segment bytes, including ID3 metadata, remain untouched.
		fmt.Fprintf(
			&playlist,
			"#EXTINF:%.9f,\n%s\n",
			segment.Duration.Seconds(),
			localName,
		)

		if onSegment != nil {
			onSegment()
		}
	}

	playlist.WriteString("#EXT-X-ENDLIST\n")

	if err := ctx.Err(); err != nil {
		return err
	}

	return os.WriteFile(destination, []byte(playlist.String()), 0600)
}
func appendTSSegment(
	ctx context.Context,
	dst io.Writer,
	path string,
) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	packet := make([]byte, 188)
	packets := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, err := io.ReadFull(file, packet)
		if err == io.EOF && n == 0 {
			if packets == 0 {
				return errors.New("goyt: empty MPEG-TS segment")
			}
			return nil
		}

		if err != nil {
			return fmt.Errorf("goyt: incomplete MPEG-TS packet: %w", err)
		}

		if packet[0] != 0x47 {
			return errors.New(
				"goyt: segment is not supported 188-byte MPEG-TS",
			)
		}

		written, err := dst.Write(packet)
		if err != nil {
			return err
		}

		if written != len(packet) {
			return io.ErrShortWrite
		}

		packets++
	}
}

// inspectHLSSegment recognizes our supported segment containers.
// This is a framing check, not a full codec or timestamp validation.
// FFmpeg parses the media and ID3 timestamps during processing.
func inspectHLSSegment(
	ctx context.Context,
	path string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var header [10]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return "", fmt.Errorf("goyt: short HLS segment: %w", err)
	}

	if header[0] == 0x47 {
		if err := appendTSSegment(ctx, io.Discard, path); err != nil {
			return "", err
		}
		return ".ts", nil
	}

	if string(header[:3]) != "ID3" {
		return "", errors.New(
			"goyt: segment is neither MPEG-TS nor supported ID3-prefixed AAC",
		)
	}

	// Initial packed-AAC support: ID3v2.3 and ID3v2.4 without flags.
	// Reject unsupported variants rather than guessing the audio offset.
	if (header[3] != 3 && header[3] != 4) ||
		header[4] != 0 || header[5] != 0 {
		return "", errors.New("goyt: unsupported packed-AAC ID3 header")
	}

	tagSize := 0
	for _, b := range header[6:10] {
		if b&0x80 != 0 {
			return "", errors.New("goyt: invalid ID3 synchsafe size")
		}
		tagSize = tagSize<<7 | int(b)
	}

	if tagSize == 0 || tagSize > 1024*1024 {
		return "", errors.New("goyt: unsupported ID3 tag size")
	}

	tag := make([]byte, tagSize)
	if _, err := io.ReadFull(file, tag); err != nil {
		return "", fmt.Errorf("goyt: truncated ID3 tag: %w", err)
	}

	// Preliminary identification only. FFmpeg interprets the actual PRIV
	// frame and timestamp; this check does not replace its ID3 parser.
	owner := []byte("com.apple.streaming.transportStreamTimestamp\x00")
	if !bytes.Contains(tag, owner) {
		return "", errors.New(
			"goyt: packed AAC lacks the expected timestamp identifier",
		)
	}

	var adts [7]byte
	if _, err := io.ReadFull(file, adts[:]); err != nil {
		return "", fmt.Errorf("goyt: missing AAC frame after ID3: %w", err)
	}

	// ADTS sync word and layer bits.
	if adts[0] != 0xff || adts[1]&0xf6 != 0xf0 {
		return "", errors.New("goyt: ID3 segment is not supported ADTS AAC")
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}

	return ".aac", nil
}
