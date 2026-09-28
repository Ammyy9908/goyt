package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ammyy9908/goyt"
	"github.com/ammyy9908/goyt/extractor/youtube"
	"github.com/ammyy9908/goyt/extractor/youtube/potprovider"
)

func runDownload(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("download", flag.ContinueOnError)
	flags.SetOutput(stderr)

	clientFlag := flags.String("client", "visionos", "YouTube client for extraction: visionos or web")
	audioLanguage := flags.String(
		"audio-language",
		"",
		"audio language tag, e.g. en or en-US; empty uses playlist/media default",
	)
	source := flags.String("url", "", "YouTube video URL")
	output := flags.String("out", "video.mp4", "output file path (default video.<container>, or audio.<ext> with -audio-only)")
	height := flags.Int("height", 1080, "maximum video height")
	videoCodec := flags.String("video-codec", "h264", "video codec for video mode: h264, vp9, av1")
	container := flags.String("container", "mp4", "output container for video mode: mp4, webm, mkv")
	transport := flags.String("transport", "http", "transport protocol: http or hls")
	decode := flags.Bool("decode-check", false, "decode the complete output after verification")
	audioOnly := flags.Bool("audio-only", false, "download audio without video")
	audioFormat := flags.String("audio-format", "best", "output format for audio-only mode: best, aac, alac, flac, m4a, mp3, opus, vorbis, wav (default best)")
	audioQuality := flags.Int("audio-quality", 2, "MP3 VBR quality: 0 (highest) to 9 (lowest), default 2")
	audioBitrate := flags.String("audio-bitrate", "", "target audio bitrate for lossy encoders, e.g. 128k, 192k")
	jsRuntimeFlag := flags.String(
		"js-runtime",
		"none",
		"JavaScript runtime for solving player cipher/n challenges: none, node, deno, bun, qjs, auto, or /path/to/binary",
	)
	poTokenProviderFlag := flags.String(
		"po-token-provider",
		"none",
		"Proof-of-Origin token provider type: none, http, or bgutil-http",
	)
	poTokenEndpointFlag := flags.String(
		"po-token-endpoint",
		"",
		"Proof-of-Origin token provider HTTP endpoint URL, e.g. http://127.0.0.1:4416",
	)
	timeout := flags.Duration("timeout", 30*time.Minute, "overall job timeout (0 disables)")
	stallTimeout := flags.Duration("stall-timeout", 60*time.Second, "network inactivity timeout per media request (0 disables)")
	urlRefreshes := flags.Int("url-refreshes", 1, "maximum URL re-extractions on expired or forbidden media (0 disables)")
	jobDir := flags.String("job-dir", "", "directory path to create and persist a new download job")
	resumeJob := flags.String("resume-job", "", "directory path of an existing persistent download job to resume")
	showVersion := flags.Bool("version", false, "print goyt version")

	flags.Usage = func() {
		fmt.Fprintln(stderr, `Usage: goyt download -url URL [options]
  goyt download -url URL [-client visionos|web] [-js-runtime none|node|deno|bun|qjs|auto|PATH] [-po-token-provider none|http|bgutil-http] [-po-token-endpoint URL] [-transport http|hls] [-video-codec h264|vp9|av1] [-container mp4|webm|mkv] [-height 1080] [-audio-language LANG] [-timeout 30m] [-stall-timeout 60s] [-url-refreshes 1] [-job-dir DIR] [-out video.<ext>] [-decode-check]
  goyt download -url URL -audio-only [-client visionos|web] [-js-runtime none|node|deno|bun|qjs|auto|PATH] [-po-token-provider none|http|bgutil-http] [-po-token-endpoint URL] [-audio-format best|aac|alac|flac|m4a|mp3|opus|vorbis|wav] [-audio-quality 0-9] [-audio-bitrate BITRATE] [-transport http|hls] [-audio-language LANG] [-timeout 30m] [-stall-timeout 60s] [-url-refreshes 1] [-job-dir DIR] [-out audio.<ext>] [-decode-check]
  goyt download -resume-job DIR [-js-runtime none|node|deno|bun|qjs|auto|PATH] [-po-token-provider none|http|bgutil-http] [-po-token-endpoint URL] [-timeout 30m] [-stall-timeout 60s] [-url-refreshes 1]
  goyt download -help`)
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		fmt.Fprintln(stdout, goyt.Version)
		return nil
	}

	var (
		clientSet                                                           bool
		heightSet, audioFormatSet, audioQualitySet, audioBitrateSet, outSet bool
		urlSet, transportSet, decodeSet, audioOnlySet, audioLanguageSet     bool
		videoCodecSet, containerSet                                         bool
		jobDirSet, resumeJobSet                                             bool
	)
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "client":
			clientSet = true
		case "url":
			urlSet = true
		case "out":
			outSet = true
		case "height":
			heightSet = true
		case "video-codec":
			videoCodecSet = true
		case "container":
			containerSet = true
		case "transport":
			transportSet = true
		case "decode-check":
			decodeSet = true
		case "audio-only":
			audioOnlySet = true
		case "audio-format":
			audioFormatSet = true
		case "audio-quality":
			audioQualitySet = true
		case "audio-bitrate":
			audioBitrateSet = true
		case "audio-language":
			audioLanguageSet = true
		case "job-dir":
			jobDirSet = true
		case "resume-job":
			resumeJobSet = true
		}
	})

	if jobDirSet && resumeJobSet {
		return errors.New("-job-dir and -resume-job are mutually exclusive")
	}

	if *timeout < 0 {
		return errors.New("timeout cannot be negative")
	}
	if *stallTimeout < 0 {
		return errors.New("stall-timeout cannot be negative")
	}
	if *urlRefreshes < 0 {
		return errors.New("url-refreshes cannot be negative")
	}

	// -------------------------------------------------------------
	// Mode 1: Resume an existing persistent job (-resume-job)
	// -------------------------------------------------------------
	if resumeJobSet {
		if flags.NArg() != 0 {
			flags.Usage()
			return errors.New("usage: goyt download -resume-job DIR [options]")
		}

		// Reject source/selection/output overrides
		if clientSet {
			return errors.New("flag -client cannot be specified with -resume-job (loaded from saved job)")
		}
		if urlSet {
			return errors.New("flag -url cannot be specified with -resume-job (loaded from saved job)")
		}
		if outSet {
			return errors.New("flag -out cannot be specified with -resume-job (loaded from saved job)")
		}
		if heightSet {
			return errors.New("flag -height cannot be specified with -resume-job (loaded from saved job)")
		}
		if videoCodecSet {
			return errors.New("flag -video-codec cannot be specified with -resume-job (loaded from saved job)")
		}
		if containerSet {
			return errors.New("flag -container cannot be specified with -resume-job (loaded from saved job)")
		}
		if transportSet {
			return errors.New("flag -transport cannot be specified with -resume-job (loaded from saved job)")
		}
		if decodeSet {
			return errors.New("flag -decode-check cannot be specified with -resume-job (loaded from saved job)")
		}
		if audioOnlySet {
			return errors.New("flag -audio-only cannot be specified with -resume-job (loaded from saved job)")
		}
		if audioFormatSet {
			return errors.New("flag -audio-format cannot be specified with -resume-job (loaded from saved job)")
		}
		if audioQualitySet {
			return errors.New("flag -audio-quality cannot be specified with -resume-job (loaded from saved job)")
		}
		if audioBitrateSet {
			return errors.New("flag -audio-bitrate cannot be specified with -resume-job (loaded from saved job)")
		}
		if audioLanguageSet {
			return errors.New("flag -audio-language cannot be specified with -resume-job (loaded from saved job)")
		}

		absJobDir, err := filepath.Abs(*resumeJob)
		if err != nil {
			return err
		}
		if info, err := os.Stat(absJobDir); err != nil || !info.IsDir() {
			return fmt.Errorf("%w: %s", goyt.ErrJobNotFound, *resumeJob)
		}

		savedManifest, err := goyt.LoadJobManifest(absJobDir)
		normClient, err := youtube.ValidateClient(savedManifest.Client)
		if err != nil {
			return fmt.Errorf("%w: %v", goyt.ErrInvalidManifest, err)
		}
		resumeClient := normClient

		if *timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, *timeout)
			defer cancel()
		}

		processor, err := goyt.NewFFmpeg("")
		if err != nil {
			return err
		}
		verifier, err := goyt.NewVerifier("")
		if err != nil {
			return err
		}
		solver, err := createChallengeSolver(*jsRuntimeFlag)
		if err != nil {
			return err
		}
		poProvider, err := createPOTokenProvider(*poTokenProviderFlag, *poTokenEndpointFlag)
		if err != nil {
			return err
		}
		var extractorOpts []youtube.Option
		if solver != nil {
			extractorOpts = append(extractorOpts, youtube.WithChallengeSolver(solver))
		}
		if poProvider != nil {
			extractorOpts = append(extractorOpts, youtube.WithPOTokenProvider(poProvider))
		}
		extractor := youtube.New(nil, extractorOpts...)

		runnerOpts := goyt.JobRunnerOptions{
			ClientValidator: func(c string) error {
				_, err := youtube.ValidateClient(c)
				return err
			},
			Extractor: func(ctx context.Context, u *url.URL) (*goyt.Media, error) {
				return extractor.ExtractDownloadableWithClient(ctx, u, resumeClient)
			},
			HLSExtractor: func(ctx context.Context, u *url.URL) (goyt.Resource, *goyt.Media, error) {
				ext, err := extractor.ExtractHLSWithClient(ctx, u, resumeClient)
				if err != nil {
					return goyt.Resource{}, nil, err
				}
				return ext.Manifest, ext.Media, nil
			},
			Downloader: goyt.NewDownloader(nil),
			Processor:  processor,
			Verifier:   verifier,
			Options: goyt.DownloadOptions{
				Resume:       true,
				MaxRetries:   2,
				StallTimeout: *stallTimeout,
			},
			URLRefreshes: *urlRefreshes,
			Stdout:       stdout,
			Stderr:       stderr,
		}

		fmt.Fprintln(stdout, "Resuming persistent job from:", absJobDir)
		err = goyt.ExecuteJob(ctx, absJobDir, runnerOpts)
		if err != nil {
			if !errors.Is(err, goyt.ErrCompletedOutputMismatch) && !errors.Is(err, goyt.ErrJobLocked) && !errors.Is(err, goyt.ErrInvalidManifest) && !errors.Is(err, goyt.ErrUnsupportedManifestVersion) && !errors.Is(err, goyt.ErrInvalidJobPath) {
				execPath := ResolveExecutablePath()
				fmt.Fprintf(stderr, "To resume this job, run:\n  %s\n", FormatResumeCommand(execPath, absJobDir))
			}
			return err
		}
		return nil
	}

	// -------------------------------------------------------------
	// Mode 2 & 3: New download (persistent via -job-dir or ephemeral)
	// -------------------------------------------------------------
	if *source == "" || flags.NArg() != 0 {
		flags.Usage()
		return errors.New("usage: goyt download -url URL [options]")
	}

	*audioLanguage = strings.TrimSpace(*audioLanguage)

	if !*audioOnly {
		if audioFormatSet || audioQualitySet || audioBitrateSet {
			return errors.New("-audio-format, -audio-quality, and -audio-bitrate require -audio-only")
		}
		if *height <= 0 {
			return errors.New("height must be positive")
		}
		if *transport != "http" && *transport != "hls" {
			return errors.New("transport must be http or hls")
		}
		if *audioLanguage != "" && *transport != "hls" {
			return errors.New("-audio-language currently requires -transport hls")
		}

		normVC, err := goyt.NormalizeVideoCodec(*videoCodec)
		if err != nil {
			return err
		}
		*videoCodec = normVC

		if containerSet {
			normC, err := goyt.NormalizeContainer(*container)
			if err != nil {
				return err
			}
			*container = normC
		} else if outSet {
			normC, err := goyt.NormalizeContainer(filepath.Ext(*output))
			if err != nil {
				return errors.New("this command requires an .mp4, .webm, or .mkv output")
			}
			*container = normC
		} else {
			*container = goyt.ContainerMP4
		}

		if containerSet && outSet {
			extC, err := goyt.NormalizeContainer(filepath.Ext(*output))
			if err != nil || extC != *container {
				return fmt.Errorf("output extension %q does not match explicit container %q", filepath.Ext(*output), *container)
			}
		}

		if !outSet {
			*output = "video." + *container
		}

		if *transport == "hls" {
			if *videoCodec != goyt.VideoCodecH264 {
				return fmt.Errorf("HLS video download currently only supports H.264 video, got %q (VP9 and AV1 HLS downloads are not supported in this phase)", *videoCodec)
			}
			if *container == goyt.ContainerWebM {
				return errors.New("HLS video download does not support WebM container (supported containers: mp4, mkv)")
			}
		}

		if err := goyt.ValidateVideoCodecContainer(*videoCodec, *container); err != nil {
			return err
		}
	} else {
		if videoCodecSet || containerSet {
			return errors.New("-video-codec and -container are not supported in -audio-only mode")
		}
		if heightSet {
			return errors.New("-height is not supported in -audio-only mode")
		}
		if audioQualitySet && audioBitrateSet {
			return errors.New("explicit -audio-quality and -audio-bitrate are mutually exclusive")
		}

		*audioFormat = strings.ToLower(strings.TrimSpace(*audioFormat))
		if *audioFormat == "" {
			*audioFormat = goyt.AudioFormatBest
		}

		validFormat := false
		for _, f := range goyt.SupportedAudioFormats {
			if *audioFormat == f {
				validFormat = true
				break
			}
		}
		if !validFormat {
			return fmt.Errorf("unsupported audio format %q; supported formats are best, aac, alac, flac, m4a, mp3, opus, vorbis, wav", *audioFormat)
		}

		if audioQualitySet {
			if *audioFormat != goyt.AudioFormatMP3 {
				return fmt.Errorf("-audio-quality is only supported for mp3 format, not %q", *audioFormat)
			}
			if *audioQuality < 0 || *audioQuality > 9 {
				return errors.New("audio quality must be between 0 and 9 (inclusive); lower values request higher quality")
			}
		}

		if audioBitrateSet {
			normalizedBitrate, err := goyt.ParseAudioBitrate(*audioBitrate)
			if err != nil {
				return err
			}
			*audioBitrate = normalizedBitrate

			switch *audioFormat {
			case goyt.AudioFormatAAC, goyt.AudioFormatM4A, goyt.AudioFormatMP3, goyt.AudioFormatOpus, goyt.AudioFormatVorbis:
			default:
				return fmt.Errorf("-audio-bitrate is not supported for %s format", *audioFormat)
			}
		}

		if *transport != "http" && *transport != "hls" {
			return errors.New("transport must be http or hls")
		}

		if *audioFormat != goyt.AudioFormatBest {
			expectedExt, _ := goyt.ExpectedExtensionForFormat(*audioFormat)
			if !outSet {
				*output = "audio" + expectedExt
			} else if !strings.EqualFold(filepath.Ext(*output), expectedExt) {
				return fmt.Errorf("audio-only %s mode requires a %s output", *audioFormat, expectedExt)
			}
		}
	}

	u, err := url.Parse(*source)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	selectedClient, err := youtube.ValidateClient(*clientFlag)
	if err != nil {
		return err
	}

	solver, err := createChallengeSolver(*jsRuntimeFlag)
	if err != nil {
		return err
	}
	poProvider, err := createPOTokenProvider(*poTokenProviderFlag, *poTokenEndpointFlag)
	if err != nil {
		return err
	}
	var extractorOpts []youtube.Option
	if solver != nil {
		extractorOpts = append(extractorOpts, youtube.WithChallengeSolver(solver))
	}
	if poProvider != nil {
		extractorOpts = append(extractorOpts, youtube.WithPOTokenProvider(poProvider))
	}

	extractor := youtube.New(nil, extractorOpts...)
	if !extractor.Match(u) {
		return errors.New("unsupported YouTube URL")
	}

	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	processor, err := goyt.NewFFmpeg("")
	if err != nil {
		return err
	}
	verifier, err := goyt.NewVerifier("")
	if err != nil {
		return err
	}

	// -------------------------------------------------------------
	// Persistent Job Creation (-job-dir)
	// -------------------------------------------------------------
	if jobDirSet {
		absJobDir, err := filepath.Abs(*jobDir)
		if err != nil {
			return err
		}
		if _, err := os.Stat(absJobDir); err == nil {
			return fmt.Errorf("%w: %s", goyt.ErrJobExists, *jobDir)
		}

		targetPath, err := filepath.Abs(*output)
		if err != nil {
			return err
		}

		var qualPtr *int
		if audioQualitySet {
			q := *audioQuality
			qualPtr = &q
		}

		if err := os.MkdirAll(absJobDir, 0700); err != nil {
			return err
		}

		fmt.Fprintln(stdout, "Job directory:", absJobDir)

		var manifest *goyt.JobManifest
		var initialMedia *goyt.Media
		var initialTracks []*goyt.HLSTrack

		if *transport == "hls" {
			if *audioOnly {
				fmt.Fprintln(stdout, "Extracting YouTube HLS manifest...")
				extracted, err := extractor.ExtractHLSWithClient(ctx, u, selectedClient)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}

				fmt.Fprintln(stdout, "Title:", extracted.Media.Title)

				hlsDownloader, err := goyt.NewHLSDownloader(nil, processor)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}

				track, selectedAudio, isOriginal, warning, err := hlsDownloader.ResolveAudioLanguage(ctx, extracted.Manifest, *audioLanguage)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}

				if track != nil {
					initialTracks = []*goyt.HLSTrack{track}
				}

				resolvedSpec, err := goyt.ResolveAudioOutputSpec(*audioFormat, "aac", qualPtr, *audioBitrate)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}

				if !outSet {
					targetPath = filepath.Join(filepath.Dir(targetPath), "audio"+resolvedSpec.Extension)
				} else if !strings.EqualFold(filepath.Ext(targetPath), resolvedSpec.Extension) {
					_ = os.RemoveAll(absJobDir)
					return fmt.Errorf("audio-only %s mode requires a %s output", *audioFormat, resolvedSpec.Extension)
				}

				if !resolvedSpec.Copy && resolvedSpec.Encoder != "" {
					has, err := processor.HasEncoder(ctx, resolvedSpec.Encoder)
					if err == nil && !has {
						_ = os.RemoveAll(absJobDir)
						return fmt.Errorf("required FFmpeg audio encoder %q is not available", resolvedSpec.Encoder)
					}
				}

				audioSel := goyt.AudioSelection{
					AudioLanguage: *audioLanguage,
					AudioFormat:   *audioFormat,
					AudioQuality:  qualPtr,
					AudioBitrate:  *audioBitrate,
				}

				manifest, err = goyt.CreateHLSAudioJob(*source, extracted.Media.ID, extracted.Media.Title, targetPath, track, selectedAudio, isOriginal, warning, resolvedSpec, audioSel, *decode)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}
				manifest.Client = selectedClient
			} else {
				fmt.Fprintln(stdout, "Extracting YouTube HLS manifest...")
				extracted, err := extractor.ExtractHLSWithClient(ctx, u, selectedClient)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}

				fmt.Fprintln(stdout, "Title:", extracted.Media.Title)

				hlsDownloader, err := goyt.NewHLSDownloader(nil, processor)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}

				resolved, err := hlsDownloader.ResolvePlaylistLanguage(ctx, extracted.Manifest, *height, *audioLanguage)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}

				if resolved.Video != nil {
					initialTracks = []*goyt.HLSTrack{resolved.Video}
					if resolved.Audio != nil {
						initialTracks = append(initialTracks, resolved.Audio)
					}
				}

				videoSel := goyt.Selection{
					MaxHeight:     *height,
					VideoCodec:    "h264",
					AudioCodec:    "aac",
					Container:     *container,
					AllowSeparate: true,
					AudioLanguage: *audioLanguage,
				}

				manifest, err = goyt.CreateHLSVideoJob(*source, extracted.Media.ID, extracted.Media.Title, targetPath, resolved, videoSel, *decode)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}
				manifest.Client = selectedClient
			}
		} else {
			if *audioOnly {
				fmt.Fprintln(stdout, "Extracting direct YouTube formats...")
				res, err := extractor.ExtractDownloadableResult(ctx, u, selectedClient)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}

				initialMedia = res.Media
				fmt.Fprintln(stdout, "Title:", res.Media.Title)
				audioSel := goyt.AudioSelection{
					AudioLanguage: *audioLanguage,
					AudioFormat:   *audioFormat,
					AudioQuality:  qualPtr,
					AudioBitrate:  *audioBitrate,
				}
				plan, err := goyt.PlanAudio(res.Media, audioSel)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}
				printResultFormatDiagnostics(stderr, res, plan.Stream.ID, plan.Stream.AudioTrackID)

				if !outSet {
					targetPath = filepath.Join(filepath.Dir(targetPath), "audio"+plan.OutputSpec.Extension)
				} else if *audioFormat == goyt.AudioFormatBest {
					if !strings.EqualFold(filepath.Ext(targetPath), plan.OutputSpec.Extension) {
						_ = os.RemoveAll(absJobDir)
						return fmt.Errorf("output extension %q does not match resolved %s source (expected %s)", filepath.Ext(targetPath), plan.OutputSpec.ResolvedCodec, plan.OutputSpec.Extension)
					}
				}

				if !plan.OutputSpec.Copy && plan.OutputSpec.Encoder != "" {
					has, err := processor.HasEncoder(ctx, plan.OutputSpec.Encoder)
					if err == nil && !has {
						_ = os.RemoveAll(absJobDir)
						return fmt.Errorf("required FFmpeg audio encoder %q is not available", plan.OutputSpec.Encoder)
					}
				}

				manifest, err = goyt.CreateAudioJob(*source, res.Media.ID, res.Media.Title, res.Media.Duration, targetPath, plan, audioSel, *decode)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}
				manifest.Client = selectedClient
			} else {
				fmt.Fprintln(stdout, "Extracting direct YouTube formats...")
				res, err := extractor.ExtractDownloadableResult(ctx, u, selectedClient)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}

				initialMedia = res.Media
				fmt.Fprintln(stdout, "Title:", res.Media.Title)
				videoSel := goyt.Selection{
					MaxHeight:     *height,
					VideoCodec:    *videoCodec,
					Container:     *container,
					AllowSeparate: true,
				}
				plan, err := goyt.Plan(res.Media, videoSel)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}
				for _, s := range plan.Streams {
					printResultFormatDiagnostics(stderr, res, s.ID, s.AudioTrackID)
				}

				manifest, err = goyt.CreateVideoJob(*source, res.Media.ID, res.Media.Title, res.Media.Duration, targetPath, plan, videoSel, *decode)
				if err != nil {
					_ = os.RemoveAll(absJobDir)
					return err
				}
				manifest.Client = selectedClient
			}
		}

		if err := manifest.Save(absJobDir); err != nil {
			_ = os.RemoveAll(absJobDir)
			return err
		}

		runnerOpts := goyt.JobRunnerOptions{
			ClientValidator: func(c string) error {
				_, err := youtube.ValidateClient(c)
				return err
			},
			Extractor: func(ctx context.Context, u *url.URL) (*goyt.Media, error) {
				return extractor.ExtractDownloadableWithClient(ctx, u, selectedClient)
			},
			HLSExtractor: func(ctx context.Context, u *url.URL) (goyt.Resource, *goyt.Media, error) {
				ext, err := extractor.ExtractHLSWithClient(ctx, u, selectedClient)
				if err != nil {
					return goyt.Resource{}, nil, err
				}
				return ext.Manifest, ext.Media, nil
			},
			Downloader: goyt.NewDownloader(nil),
			Processor:  processor,
			Verifier:   verifier,
			Options: goyt.DownloadOptions{
				Resume:       true,
				MaxRetries:   2,
				StallTimeout: *stallTimeout,
			},
			URLRefreshes:             *urlRefreshes,
			Stdout:                   stdout,
			Stderr:                   stderr,
			InitialMedia:             initialMedia,
			InitialResolvedHLSTracks: initialTracks,
		}

		err = goyt.ExecuteJob(ctx, absJobDir, runnerOpts)
		if err != nil {
			if !errors.Is(err, goyt.ErrJobLocked) && !errors.Is(err, goyt.ErrInvalidManifest) && !errors.Is(err, goyt.ErrUnsupportedManifestVersion) && !errors.Is(err, goyt.ErrCompletedOutputMismatch) && !errors.Is(err, goyt.ErrInvalidJobPath) {
				execPath := ResolveExecutablePath()
				fmt.Fprintf(stderr, "To resume this job, run:\n  %s\n", FormatResumeCommand(execPath, absJobDir))
			}
			return err
		}
		return nil
	}

	var saved string
	var expected *time.Duration
	var resolvedSpec goyt.AudioOutputSpec
	var vSpec goyt.VideoVerificationSpec

	options := goyt.DownloadOptions{
		Resume:       true,
		MaxRetries:   2,
		StallTimeout: *stallTimeout,
		OnRetry: func(diag goyt.RetryDiagnostic) {
			fmt.Fprintln(stderr)
			if diag.Resumed {
				fmt.Fprintf(
					stderr,
					"Recovery attempt %d/%d after error (%s): resuming from %.2f MiB (HTTP 206 validated).\n",
					diag.Attempt,
					diag.MaxRetries,
					diag.Reason,
					float64(diag.PriorOffset)/(1024*1024),
				)
			} else {
				actionReason := diag.ActionReason
				if actionReason == "" {
					actionReason = "safe resumption not available"
				}
				fmt.Fprintf(
					stderr,
					"Recovery attempt %d/%d after error (%s): restarting from byte 0 (%s; discarded %.2f MiB partial data).\n",
					diag.Attempt,
					diag.MaxRetries,
					diag.Reason,
					actionReason,
					float64(diag.PriorOffset)/(1024*1024),
				)
			}
		},
	}

	refreshBudget := *urlRefreshes
	maxRefreshes := *urlRefreshes

	if *audioOnly {
		var qualPtr *int
		if audioQualitySet {
			q := *audioQuality
			qualPtr = &q
		}

		switch *transport {
		case "http":
			fmt.Fprintln(stdout, "Extracting direct YouTube formats...")

			res, err := extractor.ExtractDownloadableResult(ctx, u, selectedClient)
			if err != nil {
				return err
			}
			media := res.Media

			fmt.Fprintln(stdout, "Title:", media.Title)
			expected = media.Duration
			originalMediaID := media.ID

			plan, err := goyt.PlanAudio(media, goyt.AudioSelection{
				AudioLanguage: *audioLanguage,
				AudioFormat:   *audioFormat,
				AudioQuality:  qualPtr,
				AudioBitrate:  *audioBitrate,
			})
			if err != nil {
				return err
			}
			printResultFormatDiagnostics(stderr, res, plan.Stream.ID, plan.Stream.AudioTrackID)

			resolvedSpec = plan.OutputSpec
			originalStream := plan.Stream

			if !outSet {
				*output = "audio" + plan.OutputSpec.Extension
			} else if *audioFormat == goyt.AudioFormatBest {
				if !strings.EqualFold(filepath.Ext(*output), plan.OutputSpec.Extension) {
					return fmt.Errorf("output extension %q does not match resolved %s source (expected %s)", filepath.Ext(*output), plan.OutputSpec.ResolvedCodec, plan.OutputSpec.Extension)
				}
			}

			targetPath, err := filepath.Abs(*output)
			if err != nil {
				return err
			}

			if !plan.OutputSpec.Copy && plan.OutputSpec.Encoder != "" {
				has, err := processor.HasEncoder(ctx, plan.OutputSpec.Encoder)
				if err == nil && !has {
					return fmt.Errorf("required FFmpeg audio encoder %q is not available", plan.OutputSpec.Encoder)
				}
			}

			if plan.Stream.AudioTrackName != "" {
				if plan.Stream.AudioIsOriginal {
					fmt.Fprintf(
						stdout,
						"Selected original audio track: %s (%s)\n",
						plan.Stream.AudioTrackName,
						plan.Stream.Language,
					)
				} else if *audioLanguage != "" {
					fmt.Fprintf(
						stdout,
						"Selected audio track: %s (%s)\n",
						plan.Stream.AudioTrackName,
						plan.Stream.Language,
					)
				} else {
					fmt.Fprintf(
						stdout,
						"Selected default audio track: %s (%s)\n",
						plan.Stream.AudioTrackName,
						plan.Stream.Language,
					)
				}
			} else {
				fmt.Fprintf(stdout, "Selected audio format: %s\n", plan.Stream.ID)
			}

			executor, err := goyt.NewExecutor(
				goyt.NewDownloader(nil),
				processor,
			)
			if err != nil {
				return err
			}

			currentPlan := plan
			for {
				if err := ctx.Err(); err != nil {
					return err
				}

				if currentPlan.Stream.Resource.ExpiresAt != nil &&
					!currentPlan.Stream.Resource.ExpiresAt.After(time.Now()) {
					if refreshBudget <= 0 {
						return fmt.Errorf("format %s URL has expired: %w", currentPlan.Stream.ID, goyt.ErrResourceExpired)
					}
					refreshBudget--
					refreshCount := maxRefreshes - refreshBudget
					fmt.Fprintf(stderr, "Refreshing playback URLs after media access failure (%d/%d).\n", refreshCount, maxRefreshes)
					fmt.Fprintln(stderr, "Restarting the affected transfer.")

					newMedia, extractErr := extractor.ExtractDownloadableWithClient(ctx, u, selectedClient)
					if extractErr != nil {
						return extractErr
					}
					if newMedia.ID != originalMediaID {
						return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newMedia.ID, originalMediaID)
					}
					refreshedStream, matchErr := goyt.MatchRefreshedFormat(originalStream, newMedia.Formats)
					if matchErr != nil {
						return matchErr
					}
					currentPlan.Stream = *refreshedStream
					continue
				}

				stagingFile, err := os.CreateTemp(filepath.Dir(targetPath), ".goyt-cli-staged-*"+currentPlan.OutputSpec.Extension)
				if err != nil {
					return err
				}
				stagedPath := stagingFile.Name()
				_ = stagingFile.Close()
				_ = os.Remove(stagedPath)

				result, execErr := executor.ExecuteAudio(
					ctx,
					currentPlan,
					stagedPath,
					goyt.ExecuteOptions{
						Download: options,
						OnProgress: func(p goyt.ExecutionProgress) {
							fmt.Fprintf(
								stderr,
								"\rFormat %s: %.2f MiB downloaded",
								p.FormatID,
								float64(p.Download.DownloadedBytes)/(1024*1024),
							)
						},
					},
				)
				fmt.Fprintln(stderr)
				if execErr == nil {
					saved = result.Path
					if result.CleanupError != nil {
						fmt.Fprintln(stderr, "Cleanup warning:", result.CleanupError)
					}
					break
				}

				if ctx.Err() != nil {
					return ctx.Err()
				}
				if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
					return execErr
				}

				if goyt.IsRefreshTrigger(execErr) && refreshBudget > 0 {
					refreshBudget--
					refreshCount := maxRefreshes - refreshBudget
					fmt.Fprintf(stderr, "Refreshing playback URLs after media access failure (%d/%d).\n", refreshCount, maxRefreshes)
					fmt.Fprintln(stderr, "Restarting the affected transfer.")

					newMedia, extractErr := extractor.ExtractDownloadableWithClient(ctx, u, selectedClient)
					if extractErr != nil {
						return extractErr
					}
					if newMedia.ID != originalMediaID {
						return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newMedia.ID, originalMediaID)
					}
					refreshedStream, matchErr := goyt.MatchRefreshedFormat(originalStream, newMedia.Formats)
					if matchErr != nil {
						return matchErr
					}
					currentPlan.Stream = *refreshedStream
					continue
				}

				return execErr
			}

		case "hls":
			fmt.Fprintln(stdout, "Extracting YouTube HLS manifest...")

			extracted, err := extractor.ExtractHLSWithClient(ctx, u, selectedClient)
			if err != nil {
				return err
			}

			fmt.Fprintln(stdout, "Title:", extracted.Media.Title)
			expected = extracted.Media.Duration
			originalMediaID := extracted.Media.ID

			spec, err := goyt.ResolveAudioOutputSpec(
				*audioFormat,
				"aac", // HLS audio tracks are AAC
				qualPtr,
				*audioBitrate,
			)
			if err != nil {
				return err
			}
			resolvedSpec = spec

			if !outSet {
				*output = "audio" + spec.Extension
			} else if *audioFormat == goyt.AudioFormatBest {
				if !strings.EqualFold(filepath.Ext(*output), spec.Extension) {
					return fmt.Errorf("output extension %q does not match resolved %s source (expected %s)", filepath.Ext(*output), spec.ResolvedCodec, spec.Extension)
				}
			}

			targetPath, err := filepath.Abs(*output)
			if err != nil {
				return err
			}

			if !spec.Copy && spec.Encoder != "" {
				has, err := processor.HasEncoder(ctx, spec.Encoder)
				if err == nil && !has {
					return fmt.Errorf("required FFmpeg audio encoder %q is not available", spec.Encoder)
				}
			}

			downloader, err := goyt.NewHLSDownloader(nil, processor)
			if err != nil {
				return err
			}

			currentManifest := extracted.Manifest
			var selectedAudio *goyt.HLSAudioRendition
			var isOriginalAudio bool
			var audioWarning string
			var hasResolvedOnce bool

			for {
				if err := ctx.Err(); err != nil {
					return err
				}

				if currentManifest.ExpiresAt != nil &&
					!currentManifest.ExpiresAt.After(time.Now()) {
					if refreshBudget <= 0 {
						return fmt.Errorf("HLS manifest URL has expired: %w", goyt.ErrResourceExpired)
					}
					refreshBudget--
					refreshCount := maxRefreshes - refreshBudget
					fmt.Fprintf(stderr, "Refreshing playback URLs after media access failure (%d/%d).\n", refreshCount, maxRefreshes)
					fmt.Fprintln(stderr, "Restarting the affected transfer.")

					newExtracted, extractErr := extractor.ExtractHLSWithClient(ctx, u, selectedClient)
					if extractErr != nil {
						return extractErr
					}
					if newExtracted.Media.ID != originalMediaID {
						return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newExtracted.Media.ID, originalMediaID)
					}
					currentManifest = newExtracted.Manifest
					continue
				}

				stagingFile, err := os.CreateTemp(filepath.Dir(targetPath), ".goyt-cli-staged-*"+spec.Extension)
				if err != nil {
					return err
				}
				stagedPath := stagingFile.Name()
				_ = stagingFile.Close()
				_ = os.Remove(stagedPath)

				var result *goyt.HLSAudioResult
				var execErr error

				if !hasResolvedOnce {
					var track *goyt.HLSTrack
					track, selectedAudio, isOriginalAudio, audioWarning, execErr = downloader.ResolveAudioLanguage(
						ctx,
						currentManifest,
						*audioLanguage,
					)
					if execErr == nil {
						hasResolvedOnce = true
						result, execErr = downloader.DownloadAudioTrack(
							ctx,
							track,
							selectedAudio,
							isOriginalAudio,
							audioWarning,
							spec,
							stagedPath,
							options,
							func(p goyt.HLSProgress) {
								fmt.Fprintf(
									stderr,
									"\rSegments downloaded: %d/%d",
									p.CompletedSegments,
									p.TotalSegments,
								)
							},
						)
					}
				} else {
					var track *goyt.HLSTrack
					if selectedAudio != nil {
						var newAudio *goyt.HLSAudioRendition
						track, newAudio, execErr = downloader.ResolveRefreshedAudioOnlyMaster(
							ctx,
							currentManifest,
							*selectedAudio,
						)
						if execErr == nil && newAudio != nil {
							selectedAudio = newAudio
						}
					} else {
						track, execErr = downloader.ResolveMediaPlaylist(ctx, currentManifest)
					}

					if execErr == nil {
						result, execErr = downloader.DownloadAudioTrack(
							ctx,
							track,
							selectedAudio,
							isOriginalAudio,
							audioWarning,
							spec,
							stagedPath,
							options,
							func(p goyt.HLSProgress) {
								fmt.Fprintf(
									stderr,
									"\rSegments downloaded: %d/%d",
									p.CompletedSegments,
									p.TotalSegments,
								)
							},
						)
					}
				}

				fmt.Fprintln(stderr)
				if execErr == nil {
					if audioWarning != "" && *audioLanguage == "" {
						fmt.Fprintf(stderr, "Warning: %s\n", audioWarning)
					}

					if selectedAudio != nil {
						if isOriginalAudio {
							fmt.Fprintf(
								stdout,
								"Selected original audio track: %s (%s)\n",
								selectedAudio.Name,
								selectedAudio.Language,
							)
						} else if *audioLanguage != "" {
							fmt.Fprintf(
								stdout,
								"Selected audio track: %s (%s)\n",
								selectedAudio.Name,
								selectedAudio.Language,
							)
						} else {
							fmt.Fprintf(
								stdout,
								"Selected default audio track: %s (%s)\n",
								selectedAudio.Name,
								selectedAudio.Language,
							)
						}
					}

					saved = result.Path
					if expected == nil {
						duration := result.PlaylistDuration
						expected = &duration
					}
					if result.CleanupError != nil {
						fmt.Fprintln(stderr, "Cleanup warning:", result.CleanupError)
					}
					break
				}

				_ = os.Remove(stagedPath)

				if ctx.Err() != nil {
					return ctx.Err()
				}
				if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
					return execErr
				}

				if goyt.IsRefreshTrigger(execErr) && refreshBudget > 0 {
					refreshBudget--
					refreshCount := maxRefreshes - refreshBudget
					fmt.Fprintf(stderr, "Refreshing playback URLs after media access failure (%d/%d).\n", refreshCount, maxRefreshes)
					fmt.Fprintln(stderr, "Restarting the affected transfer.")

					newExtracted, extractErr := extractor.ExtractHLSWithClient(ctx, u, selectedClient)
					if extractErr != nil {
						return extractErr
					}
					if newExtracted.Media.ID != originalMediaID {
						return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newExtracted.Media.ID, originalMediaID)
					}
					currentManifest = newExtracted.Manifest
					continue
				}

				return execErr
			}
		}

		targetPath, err := filepath.Abs(*output)
		if err != nil {
			return err
		}

		fmt.Fprintln(stdout, "Verifying audio output streams and duration...")
		if _, err := verifier.VerifyAudio(ctx, saved, resolvedSpec, expected); err != nil {
			return fmt.Errorf("verification failed; output retained: %w", err)
		}

		if *decode {
			fmt.Fprintln(stdout, "Decoding the complete audio output...")
			if err := processor.CheckDecodeAudio(ctx, saved); err != nil {
				return fmt.Errorf("decode failed; output retained: %w", err)
			}
			fmt.Fprintln(stdout, "Full audio decode check passed.")
		}

		if err := os.Rename(saved, targetPath); err != nil {
			return fmt.Errorf("failed to commit destination file: %w", err)
		}

		fmt.Fprintln(stdout, "Saved and verified:", targetPath)
		return nil
	}

	// Video download path
	targetPath, err := filepath.Abs(*output)
	if err != nil {
		return err
	}

	switch *transport {
	case "http":
		fmt.Fprintln(stdout, "Extracting direct YouTube formats...")

		res, err := extractor.ExtractDownloadableResult(ctx, u, selectedClient)
		if err != nil {
			return err
		}
		media := res.Media

		fmt.Fprintln(stdout, "Title:", media.Title)
		expected = media.Duration
		originalMediaID := media.ID

		plan, err := goyt.Plan(media, goyt.Selection{
			MaxHeight:     *height,
			VideoCodec:    *videoCodec,
			Container:     *container,
			AllowSeparate: true,
		})
		if err != nil {
			return err
		}
		for _, s := range plan.Streams {
			printResultFormatDiagnostics(stderr, res, s.ID, s.AudioTrackID)
		}

		originalStreams := append([]goyt.Format(nil), plan.Streams...)

		for i, stream := range plan.Streams {
			if stream.VideoCodec != "" && stream.AudioCodec != "" && stream.AudioCodec != "none" {
				fmt.Fprintf(stdout, "Input %d: format %s (video: %s, audio: %s)\n", i+1, stream.ID, stream.VideoCodec, stream.AudioCodec)
			} else if stream.VideoCodec != "" && stream.VideoCodec != "none" {
				fmt.Fprintf(stdout, "Input %d: format %s (video: %s)\n", i+1, stream.ID, stream.VideoCodec)
			} else if stream.AudioCodec != "" && stream.AudioCodec != "none" {
				fmt.Fprintf(stdout, "Input %d: format %s (audio: %s)\n", i+1, stream.ID, stream.AudioCodec)
			} else {
				fmt.Fprintf(stdout, "Input %d: format %s\n", i+1, stream.ID)
			}
		}
		fmt.Fprintf(stdout, "Output container: %s\n", plan.OutputContainer)

		var vCodec, aCodec string
		var w, h *int
		if len(plan.Streams) > 0 {
			vCodec = plan.Streams[0].VideoCodec
			w = plan.Streams[0].Width
			h = plan.Streams[0].Height
			if len(plan.Streams) > 1 {
				aCodec = plan.Streams[1].AudioCodec
			} else {
				aCodec = plan.Streams[0].AudioCodec
			}
		}
		vSpec = goyt.VideoVerificationSpec{
			ExpectedContainer:  plan.OutputContainer,
			ExpectedVideoCodec: vCodec,
			ExpectedAudioCodec: aCodec,
			ExpectedWidth:      w,
			ExpectedHeight:     h,
		}

		executor, err := goyt.NewExecutor(
			goyt.NewDownloader(nil),
			processor,
		)
		if err != nil {
			return err
		}

		currentPlan := plan
		for {
			if err := ctx.Err(); err != nil {
				return err
			}

			hasExpiredStream := false
			for _, stream := range currentPlan.Streams {
				if stream.Resource.ExpiresAt != nil && !stream.Resource.ExpiresAt.After(time.Now()) {
					hasExpiredStream = true
					break
				}
			}

			if hasExpiredStream {
				if refreshBudget <= 0 {
					return goyt.ErrResourceExpired
				}
				refreshBudget--
				refreshCount := maxRefreshes - refreshBudget
				fmt.Fprintf(stderr, "Refreshing playback URLs after media access failure (%d/%d).\n", refreshCount, maxRefreshes)
				fmt.Fprintln(stderr, "Restarting the affected transfer.")

				newMedia, extractErr := extractor.ExtractDownloadableWithClient(ctx, u, selectedClient)
				if extractErr != nil {
					return extractErr
				}
				if newMedia.ID != originalMediaID {
					return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newMedia.ID, originalMediaID)
				}

				newStreams := make([]goyt.Format, len(originalStreams))
				for i, orig := range originalStreams {
					matched, matchErr := goyt.MatchRefreshedFormat(orig, newMedia.Formats)
					if matchErr != nil {
						return matchErr
					}
					newStreams[i] = *matched
				}
				currentPlan.Streams = newStreams
				continue
			}

			stagingFile, err := os.CreateTemp(filepath.Dir(targetPath), ".goyt-cli-staged-*."+plan.OutputContainer)
			if err != nil {
				return err
			}
			stagedPath := stagingFile.Name()
			_ = stagingFile.Close()
			_ = os.Remove(stagedPath)

			result, execErr := executor.Execute(
				ctx,
				currentPlan,
				stagedPath,
				goyt.ExecuteOptions{
					Download: options,
					OnProgress: func(p goyt.ExecutionProgress) {
						fmt.Fprintf(
							stderr,
							"\rFormat %s: %.2f MiB downloaded",
							p.FormatID,
							float64(p.Download.DownloadedBytes)/(1024*1024),
						)
					},
				},
			)
			fmt.Fprintln(stderr)
			if execErr == nil {
				saved = result.Path
				if result.CleanupError != nil {
					fmt.Fprintln(stderr, "Cleanup warning:", result.CleanupError)
				}
				break
			}

			_ = os.Remove(stagedPath)

			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
				return execErr
			}

			if goyt.IsRefreshTrigger(execErr) && refreshBudget > 0 {
				refreshBudget--
				refreshCount := maxRefreshes - refreshBudget
				fmt.Fprintf(stderr, "Refreshing playback URLs after media access failure (%d/%d).\n", refreshCount, maxRefreshes)
				fmt.Fprintln(stderr, "Restarting the affected transfer.")

				newMedia, extractErr := extractor.ExtractDownloadableWithClient(ctx, u, selectedClient)
				if extractErr != nil {
					return extractErr
				}
				if newMedia.ID != originalMediaID {
					return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newMedia.ID, originalMediaID)
				}

				newStreams := make([]goyt.Format, len(originalStreams))
				for i, orig := range originalStreams {
					matched, matchErr := goyt.MatchRefreshedFormat(orig, newMedia.Formats)
					if matchErr != nil {
						return matchErr
					}
					newStreams[i] = *matched
				}
				currentPlan.Streams = newStreams
				continue
			}

			return execErr
		}

	case "hls":
		fmt.Fprintln(stdout, "Extracting YouTube HLS manifest...")

		extracted, err := extractor.ExtractHLSWithClient(ctx, u, selectedClient)
		if err != nil {
			return err
		}

		fmt.Fprintln(stdout, "Title:", extracted.Media.Title)
		expected = extracted.Media.Duration
		originalMediaID := extracted.Media.ID

		downloader, err := goyt.NewHLSDownloader(nil, processor)
		if err != nil {
			return err
		}

		currentManifest := extracted.Manifest
		var selectedVariant *goyt.HLSVariant
		var selectedAudio *goyt.HLSAudioRendition
		var isOriginalAudio bool
		var audioWarning string
		var hasResolvedOnce bool

		for {
			if err := ctx.Err(); err != nil {
				return err
			}

			if currentManifest.ExpiresAt != nil &&
				!currentManifest.ExpiresAt.After(time.Now()) {
				if refreshBudget <= 0 {
					return fmt.Errorf("HLS manifest URL has expired: %w", goyt.ErrResourceExpired)
				}
				refreshBudget--
				refreshCount := maxRefreshes - refreshBudget
				fmt.Fprintf(stderr, "Refreshing playback URLs after media access failure (%d/%d).\n", refreshCount, maxRefreshes)
				fmt.Fprintln(stderr, "Restarting the affected transfer.")

				newExtracted, extractErr := extractor.ExtractHLSWithClient(ctx, u, selectedClient)
				if extractErr != nil {
					return extractErr
				}
				if newExtracted.Media.ID != originalMediaID {
					return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newExtracted.Media.ID, originalMediaID)
				}
				currentManifest = newExtracted.Manifest
				continue
			}

			vSpec = goyt.VideoVerificationSpec{
				ExpectedContainer:  *container,
				ExpectedVideoCodec: "h264",
				ExpectedAudioCodec: "aac",
			}

			stagingFile, err := os.CreateTemp(filepath.Dir(targetPath), ".goyt-cli-staged-*."+*container)
			if err != nil {
				return err
			}
			stagedPath := stagingFile.Name()
			_ = stagingFile.Close()
			_ = os.Remove(stagedPath)

			var result *goyt.HLSResult
			var execErr error

			if !hasResolvedOnce {
				var resolved *goyt.ResolvedHLS
				resolved, execErr = downloader.ResolvePlaylistLanguage(
					ctx,
					currentManifest,
					*height,
					*audioLanguage,
				)
				if execErr == nil {
					selectedVariant = resolved.SelectedVariant
					selectedAudio = resolved.SelectedAudio
					isOriginalAudio = resolved.AudioIsOriginal
					audioWarning = resolved.AudioWarning
					hasResolvedOnce = true

					result, execErr = downloader.DownloadResolved(
						ctx,
						resolved,
						stagedPath,
						options,
						func(p goyt.HLSProgress) {
							fmt.Fprintf(
								stderr,
								"\rSegments downloaded: %d/%d",
								p.CompletedSegments,
								p.TotalSegments,
							)
						},
					)
				}
			} else {
				var resolved *goyt.ResolvedHLS
				if selectedVariant != nil {
					resolved, execErr = downloader.ResolveRefreshedMaster(
						ctx,
						currentManifest,
						*selectedVariant,
						selectedAudio,
					)
				} else {
					var track *goyt.HLSTrack
					track, execErr = downloader.ResolveMediaPlaylist(ctx, currentManifest)
					if execErr == nil {
						resolved = &goyt.ResolvedHLS{Video: track}
					}
				}

				if execErr == nil {
					result, execErr = downloader.DownloadResolved(
						ctx,
						resolved,
						stagedPath,
						options,
						func(p goyt.HLSProgress) {
							fmt.Fprintf(
								stderr,
								"\rSegments downloaded: %d/%d",
								p.CompletedSegments,
								p.TotalSegments,
							)
						},
					)
				}
			}

			fmt.Fprintln(stderr)
			if execErr == nil {
				if audioWarning != "" && *audioLanguage == "" {
					fmt.Fprintf(stderr, "Warning: %s\n", audioWarning)
				}

				if selectedAudio != nil {
					if isOriginalAudio {
						fmt.Fprintf(
							stdout,
							"Selected original audio track: %s (%s)\n",
							selectedAudio.Name,
							selectedAudio.Language,
						)
					} else if *audioLanguage != "" {
						fmt.Fprintf(
							stdout,
							"Selected audio track: %s (%s)\n",
							selectedAudio.Name,
							selectedAudio.Language,
						)
					} else {
						fmt.Fprintf(
							stdout,
							"Selected default audio track: %s (%s)\n",
							selectedAudio.Name,
							selectedAudio.Language,
						)
					}
				}

				saved = result.Path
				if expected == nil {
					duration := result.PlaylistDuration
					expected = &duration
				}
				if result.CleanupError != nil {
					fmt.Fprintln(stderr, "Cleanup warning:", result.CleanupError)
				}
				break
			}

			_ = os.Remove(stagedPath)

			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
				return execErr
			}

			if goyt.IsRefreshTrigger(execErr) && refreshBudget > 0 {
				refreshBudget--
				refreshCount := maxRefreshes - refreshBudget
				fmt.Fprintf(stderr, "Refreshing playback URLs after media access failure (%d/%d).\n", refreshCount, maxRefreshes)
				fmt.Fprintln(stderr, "Restarting the affected transfer.")

				newExtracted, extractErr := extractor.ExtractHLSWithClient(ctx, u, selectedClient)
				if extractErr != nil {
					return extractErr
				}
				if newExtracted.Media.ID != originalMediaID {
					return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newExtracted.Media.ID, originalMediaID)
				}
				currentManifest = newExtracted.Manifest
				continue
			}

			return execErr
		}
	}

	fmt.Fprintln(stdout, "Verifying output streams and duration...")
	if _, err := verifier.VerifyVideo(ctx, saved, vSpec, expected); err != nil {
		return fmt.Errorf("verification failed; output retained: %w", err)
	}

	if *decode {
		fmt.Fprintln(stdout, "Decoding the complete output...")
		if err := processor.CheckDecode(ctx, saved); err != nil {
			return fmt.Errorf("decode failed; output retained: %w", err)
		}
		fmt.Fprintln(stdout, "Full decode check passed.")
	}

	if err := os.Rename(saved, targetPath); err != nil {
		return fmt.Errorf("failed to commit destination file: %w", err)
	}

	fmt.Fprintln(stdout, "Saved and verified:", targetPath)
	return nil
}

func printResultFormatDiagnostics(stderr io.Writer, res *youtube.DownloadableResult, formatID string, audioTrackID ...string) {
	if res == nil || stderr == nil {
		return
	}
	diag, _ := res.FormatDiagnostics(formatID, audioTrackID...)
	fmt.Fprintln(stderr, diag.String())
}

func printSelectedFormatDiagnostics(stderr io.Writer, extractor *youtube.Extractor, formatIDs ...string) {
	if extractor == nil || stderr == nil {
		return
	}
	for _, fid := range formatIDs {
		if diag, ok := extractor.FormatDiagnostics(fid); ok {
			fmt.Fprintln(stderr, diag.String())
		} else {
			fmt.Fprintln(stderr, diag.String())
		}
	}
}

func createPOTokenProvider(providerType, endpoint string) (youtube.POTokenProvider, error) {
	normType := strings.ToLower(strings.TrimSpace(providerType))
	trimmedEndpoint := strings.TrimSpace(endpoint)

	if normType == "" || normType == "none" {
		if trimmedEndpoint != "" {
			return nil, errors.New("flag -po-token-endpoint requires -po-token-provider to be configured (e.g. -po-token-provider http)")
		}
		return nil, nil
	}

	switch normType {
	case "http", "bgutil-http":
		if trimmedEndpoint == "" {
			return nil, errors.New("flag -po-token-endpoint is required when -po-token-provider is enabled")
		}
		p, err := potprovider.NewHTTPProvider(trimmedEndpoint)
		if err != nil {
			return nil, err
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unknown -po-token-provider %q: supported types are none, http, bgutil-http", providerType)
	}
}
