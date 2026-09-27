package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
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
	timeout := flags.Duration("timeout", 30*time.Minute, "overall job timeout (0 disables)")
	stallTimeout := flags.Duration("stall-timeout", 60*time.Second, "network inactivity timeout per media request (0 disables)")

	flags.Usage = func() {
		fmt.Fprintln(stderr, `Usage: goyt hls -url PLAYLIST_URL [options]
  goyt hls -url PLAYLIST_URL [-height 1080] [-timeout 30m] [-stall-timeout 60s] [-out hls.mp4]
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

	if *timeout < 0 {
		return errors.New("timeout cannot be negative")
	}
	if *stallTimeout < 0 {
		return errors.New("stall-timeout cannot be negative")
	}

	if !strings.EqualFold(filepath.Ext(*output), ".mp4") {
		return errors.New("this command requires an .mp4 output")
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

	downloader, err := goyt.NewHLSDownloader(nil, processor)
	if err != nil {
		return err
	}

	targetPath, err := filepath.Abs(*output)
	if err != nil {
		return err
	}

	stagingFile, err := os.CreateTemp(filepath.Dir(targetPath), ".goyt-cli-staged-*.mp4")
	if err != nil {
		return err
	}
	stagedPath := stagingFile.Name()
	_ = stagingFile.Close()
	_ = os.Remove(stagedPath)
	defer os.Remove(stagedPath)

	result, err := downloader.Download(
		ctx,
		goyt.Resource{URL: *source},
		stagedPath,
		*maxHeight,
		goyt.DownloadOptions{
			Resume:       true,
			MaxRetries:   2,
			StallTimeout: *stallTimeout,
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

	if err := os.Rename(result.Path, targetPath); err != nil {
		return fmt.Errorf("failed to commit destination file: %w", err)
	}

	fmt.Fprintf(stdout, "Saved and verified %s\n", targetPath)
	return nil
}
