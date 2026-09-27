package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/ammyy9908/goyt"
)

func runHLS(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("hls", flag.ContinueOnError)
	flags.SetOutput(stderr)

	source := flags.String("url", "", "completed MPEG-TS media playlist URL")
	output := flags.String("out", "hls.mp4", "output MP4 path")
	maxHeight := flags.Int("height", 1080, "maximum master-playlist variant height")

	flags.Usage = func() {
		fmt.Fprintln(stderr, `Usage: goyt hls -url PLAYLIST_URL [options]
  goyt hls -url PLAYLIST_URL [-height 1080] [-out hls.mp4]
  goyt hls -help`)
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return err
	}

	if *source == "" || flags.NArg() != 0 || *maxHeight <= 0 {
		flags.Usage()
		return errors.New("usage: goyt hls -url PLAYLIST_URL -out FILE.mp4 [-height 1080]")
	}

	if !strings.EqualFold(filepath.Ext(*output), ".mp4") {
		return errors.New("this command requires an .mp4 output")
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

	downloader, err := goyt.NewHLSDownloader(nil, processor)
	if err != nil {
		return err
	}

	result, err := downloader.Download(
		ctx,
		goyt.Resource{URL: *source},
		*output,
		*maxHeight,
		goyt.DownloadOptions{
			Resume:     true,
			MaxRetries: 2,
		},
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

	if result.CleanupError != nil {
		fmt.Fprintf(stderr, "Cleanup warning: %v\n", result.CleanupError)
	}

	fmt.Fprintln(stdout, "Verifying output...")

	if _, err := verifier.VerifyMP4(
		ctx, result.Path, &result.PlaylistDuration,
	); err != nil {
		return fmt.Errorf("verification failed; output retained: %w", err)
	}

	if err := processor.CheckDecode(ctx, result.Path); err != nil {
		return fmt.Errorf("decode failed; output retained: %w", err)
	}

	fmt.Fprintf(stdout, "Saved and verified %s\n", result.Path)
	return nil
}
