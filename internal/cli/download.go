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
	showVersion := flags.Bool("version", false, "print goyt version")

	flags.Usage = func() {
		fmt.Fprintln(stderr, `Usage: goyt download -url URL [options]
  goyt download -url URL [-transport http|hls] [-height 1080] [-audio-language LANG] [-out video.mp4] [-decode-check]
  goyt download -url URL -audio-only [-audio-format best|aac|alac|flac|m4a|mp3|opus|vorbis|wav] [-audio-quality 0-9] [-audio-bitrate BITRATE] [-transport http|hls] [-audio-language LANG] [-out audio.<ext>] [-decode-check]
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

	u, err := url.Parse(*source)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	extractor := youtube.New(nil)
	if !extractor.Match(u) {
		return errors.New("unsupported YouTube URL")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

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
		Resume:     true,
		MaxRetries: 2,
	}

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

			if plan.Stream.Resource.ExpiresAt != nil &&
				!plan.Stream.Resource.ExpiresAt.After(time.Now()) {
				return fmt.Errorf("format %s URL has expired", plan.Stream.ID)
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

			stagingFile, err := os.CreateTemp(filepath.Dir(targetPath), ".goyt-cli-staged-*"+plan.OutputSpec.Extension)
			if err != nil {
				return err
			}
			stagedPath := stagingFile.Name()
			_ = stagingFile.Close()
			_ = os.Remove(stagedPath)
			defer os.Remove(stagedPath)

			result, err := executor.ExecuteAudio(
				ctx,
				plan,
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
			if err != nil {
				return err
			}

			saved = result.Path
			if result.CleanupError != nil {
				fmt.Fprintln(stderr, "Cleanup warning:", result.CleanupError)
			}

		case "hls":
			fmt.Fprintln(stdout, "Extracting YouTube HLS manifest...")

			extracted, err := extractor.ExtractHLS(ctx, u)
			if err != nil {
				return err
			}

			fmt.Fprintln(stdout, "Title:", extracted.Media.Title)
			expected = extracted.Media.Duration

			if expiry := extracted.Manifest.ExpiresAt; expiry != nil &&
				!expiry.After(time.Now()) {
				return errors.New("HLS manifest URL has expired")
			}

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

			stagingFile, err := os.CreateTemp(filepath.Dir(targetPath), ".goyt-cli-staged-*"+spec.Extension)
			if err != nil {
				return err
			}
			stagedPath := stagingFile.Name()
			_ = stagingFile.Close()
			_ = os.Remove(stagedPath)
			defer os.Remove(stagedPath)

			result, err := downloader.DownloadAudio(
				ctx,
				extracted.Manifest,
				stagedPath,
				*audioLanguage,
				spec,
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
			fmt.Fprintln(stderr)
			if err != nil {
				return err
			}

			if result.AudioWarning != "" && *audioLanguage == "" {
				fmt.Fprintf(stderr, "Warning: %s\n", result.AudioWarning)
			}

			if result.SelectedAudio != nil {
				if result.AudioIsOriginal {
					fmt.Fprintf(
						stdout,
						"Selected original audio track: %s (%s)\n",
						result.SelectedAudio.Name,
						result.SelectedAudio.Language,
					)
				} else if *audioLanguage != "" {
					fmt.Fprintf(
						stdout,
						"Selected audio track: %s (%s)\n",
						result.SelectedAudio.Name,
						result.SelectedAudio.Language,
					)
				} else {
					fmt.Fprintf(
						stdout,
						"Selected default audio track: %s (%s)\n",
						result.SelectedAudio.Name,
						result.SelectedAudio.Language,
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

	// Video download path (existing behavior preserved)
	switch *transport {
	case "http":
		fmt.Fprintln(stdout, "Extracting direct YouTube formats...")

		media, err := extractor.ExtractDownloadable(ctx, u)
		if err != nil {
			return err
		}

		fmt.Fprintln(stdout, "Title:", media.Title)
		expected = media.Duration

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

		for i, stream := range plan.Streams {
			if stream.Resource.ExpiresAt != nil &&
				!stream.Resource.ExpiresAt.After(time.Now()) {
				return fmt.Errorf("format %s URL has expired", stream.ID)
			}

			fmt.Fprintf(stdout, "Input %d: format %s\n", i+1, stream.ID)
		}

		executor, err := goyt.NewExecutor(
			goyt.NewDownloader(nil),
			processor,
		)
		if err != nil {
			return err
		}

		result, err := executor.Execute(
			ctx,
			plan,
			*output,
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
		if err != nil {
			return err
		}

		saved = result.Path
		if result.CleanupError != nil {
			fmt.Fprintln(stderr, "Cleanup warning:", result.CleanupError)
		}

	case "hls":
		fmt.Fprintln(stdout, "Extracting YouTube HLS manifest...")

		extracted, err := extractor.ExtractHLS(ctx, u)
		if err != nil {
			return err
		}

		fmt.Fprintln(stdout, "Title:", extracted.Media.Title)
		expected = extracted.Media.Duration

		if expiry := extracted.Manifest.ExpiresAt; expiry != nil &&
			!expiry.After(time.Now()) {
			return errors.New("HLS manifest URL has expired")
		}

		downloader, err := goyt.NewHLSDownloader(nil, processor)
		if err != nil {
			return err
		}

		result, err := downloader.DownloadWithAudioLanguage(
			ctx,
			extracted.Manifest,
			*output,
			*height,
			*audioLanguage,
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
		fmt.Fprintln(stderr)
		if err != nil {
			return err
		}

		if result.AudioWarning != "" && *audioLanguage == "" {
			fmt.Fprintf(stderr, "Warning: %s\n", result.AudioWarning)
		}

		if result.SelectedAudio != nil {
			if result.AudioIsOriginal {
				fmt.Fprintf(
					stdout,
					"Selected original audio track: %s (%s)\n",
					result.SelectedAudio.Name,
					result.SelectedAudio.Language,
				)
			} else if *audioLanguage != "" {
				fmt.Fprintf(
					stdout,
					"Selected audio track: %s (%s)\n",
					result.SelectedAudio.Name,
					result.SelectedAudio.Language,
				)
			} else {
				fmt.Fprintf(
					stdout,
					"Selected default audio track: %s (%s)\n",
					result.SelectedAudio.Name,
					result.SelectedAudio.Language,
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

	fmt.Fprintln(stdout, "Saved and verified:", saved)
	return nil
}
