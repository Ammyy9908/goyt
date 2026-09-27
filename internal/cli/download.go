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
)

func runDownload(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("download", flag.ContinueOnError)
	flags.SetOutput(stderr)

	audioLanguage := flags.String(
		"audio-language",
		"",
		"audio language tag, e.g. en or en-US; empty uses playlist/media default",
	)
	source := flags.String("url", "", "YouTube video URL")
	output := flags.String("out", "video.mp4", "output file path (default video.mp4, or audio.<ext> with -audio-only)")
	height := flags.Int("height", 1080, "maximum video height")
	transport := flags.String("transport", "http", "transport protocol: http or hls")
	decode := flags.Bool("decode-check", false, "decode the complete output after verification")
	audioOnly := flags.Bool("audio-only", false, "download audio without video")
	audioFormat := flags.String("audio-format", "best", "output format for audio-only mode: best, aac, alac, flac, m4a, mp3, opus, vorbis, wav (default best)")
	audioQuality := flags.Int("audio-quality", 2, "MP3 VBR quality: 0 (highest) to 9 (lowest), default 2")
	audioBitrate := flags.String("audio-bitrate", "", "target audio bitrate for lossy encoders, e.g. 128k, 192k")
	timeout := flags.Duration("timeout", 30*time.Minute, "overall job timeout (0 disables)")
	stallTimeout := flags.Duration("stall-timeout", 60*time.Second, "network inactivity timeout per media request (0 disables)")
	urlRefreshes := flags.Int("url-refreshes", 1, "maximum URL re-extractions on expired or forbidden media (0 disables)")
	showVersion := flags.Bool("version", false, "print goyt version")

	flags.Usage = func() {
		fmt.Fprintln(stderr, `Usage: goyt download -url URL [options]
  goyt download -url URL [-transport http|hls] [-height 1080] [-audio-language LANG] [-timeout 30m] [-stall-timeout 60s] [-url-refreshes 1] [-out video.mp4] [-decode-check]
  goyt download -url URL -audio-only [-audio-format best|aac|alac|flac|m4a|mp3|opus|vorbis|wav] [-audio-quality 0-9] [-audio-bitrate BITRATE] [-transport http|hls] [-audio-language LANG] [-timeout 30m] [-stall-timeout 60s] [-url-refreshes 1] [-out audio.<ext>] [-decode-check]
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

	if *source == "" || flags.NArg() != 0 {
		flags.Usage()
		return errors.New("usage: goyt download -url URL [options]")
	}

	var heightSet, audioFormatSet, audioQualitySet, audioBitrateSet, outSet bool
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "height":
			heightSet = true
		case "audio-format":
			audioFormatSet = true
		case "audio-quality":
			audioQualitySet = true
		case "audio-bitrate":
			audioBitrateSet = true
		case "out":
			outSet = true
		}
	})

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
		if !strings.EqualFold(filepath.Ext(*output), ".mp4") {
			return errors.New("this command requires an .mp4 output")
		}
	} else {
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

	if *timeout < 0 {
		return errors.New("timeout cannot be negative")
	}
	if *stallTimeout < 0 {
		return errors.New("stall-timeout cannot be negative")
	}
	if *urlRefreshes < 0 {
		return errors.New("url-refreshes cannot be negative")
	}

	u, err := url.Parse(*source)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	extractor := youtube.New(nil)
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

	var saved string
	var expected *time.Duration
	var resolvedSpec goyt.AudioOutputSpec

	options := goyt.DownloadOptions{
		Resume:       true,
		MaxRetries:   2,
		StallTimeout: *stallTimeout,
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

			media, err := extractor.ExtractDownloadable(ctx, u)
			if err != nil {
				return err
			}

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

					newMedia, extractErr := extractor.ExtractDownloadable(ctx, u)
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

					newMedia, extractErr := extractor.ExtractDownloadable(ctx, u)
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

			extracted, err := extractor.ExtractHLS(ctx, u)
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

					newExtracted, extractErr := extractor.ExtractHLS(ctx, u)
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

					newExtracted, extractErr := extractor.ExtractHLS(ctx, u)
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

		media, err := extractor.ExtractDownloadable(ctx, u)
		if err != nil {
			return err
		}

		fmt.Fprintln(stdout, "Title:", media.Title)
		expected = media.Duration
		originalMediaID := media.ID

		plan, err := goyt.Plan(media, goyt.Selection{
			MaxHeight:     *height,
			VideoCodec:    "h264",
			AudioCodec:    "aac",
			Container:     "mp4",
			AllowSeparate: true,
		})
		if err != nil {
			return err
		}

		originalStreams := append([]goyt.Format(nil), plan.Streams...)

		for i, stream := range plan.Streams {
			fmt.Fprintf(stdout, "Input %d: format %s\n", i+1, stream.ID)
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

				newMedia, extractErr := extractor.ExtractDownloadable(ctx, u)
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

			stagingFile, err := os.CreateTemp(filepath.Dir(targetPath), ".goyt-cli-staged-*.mp4")
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

				newMedia, extractErr := extractor.ExtractDownloadable(ctx, u)
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

		extracted, err := extractor.ExtractHLS(ctx, u)
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

				newExtracted, extractErr := extractor.ExtractHLS(ctx, u)
				if extractErr != nil {
					return extractErr
				}
				if newExtracted.Media.ID != originalMediaID {
					return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newExtracted.Media.ID, originalMediaID)
				}
				currentManifest = newExtracted.Manifest
				continue
			}

			stagingFile, err := os.CreateTemp(filepath.Dir(targetPath), ".goyt-cli-staged-*.mp4")
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

				newExtracted, extractErr := extractor.ExtractHLS(ctx, u)
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
	if _, err := verifier.VerifyMP4(ctx, saved, expected); err != nil {
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
