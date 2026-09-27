package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
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
		"HLS audio language, e.g. en or en-US; empty uses playlist default",
	)
	source := flags.String("url", "", "YouTube video URL")
	output := flags.String("out", "video.mp4", "output MP4 path")
	height := flags.Int("height", 1080, "maximum video height")
	transport := flags.String("transport", "http", "transport protocol: http or hls")
	decode := flags.Bool("decode-check", false, "decode the complete output after verification")
	showVersion := flags.Bool("version", false, "print goyt version")

	flags.Usage = func() {
		fmt.Fprintln(stderr, `Usage: goyt download -url URL [options]
  goyt download -url URL [-transport http|hls] [-height 1080] [-audio-language LANG] [-out video.mp4] [-decode-check]
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
		return errors.New("usage: goyt download -url URL -transport http|hls -height 1080 -out video.mp4")
	}
	if *height <= 0 {
		return errors.New("height must be positive")
	}
	if *transport != "http" && *transport != "hls" {
		return errors.New("transport must be http or hls")
	}

	*audioLanguage = strings.TrimSpace(*audioLanguage)
	if *audioLanguage != "" && *transport != "hls" {
		return errors.New("-audio-language currently requires -transport hls")
	}
	if !strings.EqualFold(filepath.Ext(*output), ".mp4") {
		return errors.New("this command requires an .mp4 output")
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

	options := goyt.DownloadOptions{
		Resume:     true,
		MaxRetries: 2,
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
