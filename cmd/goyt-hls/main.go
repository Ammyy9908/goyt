package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/ammyy9908/goyt"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "\ngoyt:", err)
		os.Exit(1)
	}
}

func run() error {
	source := flag.String("url", "", "completed MPEG-TS media playlist URL")
	output := flag.String("out", "hls.mp4", "output MP4 path")
	maxHeight := flag.Int("height", 1080, "maximum master-playlist variant height")
	flag.Parse()

	if *source == "" || flag.NArg() != 0 || *maxHeight <= 0 {
		return fmt.Errorf(
			"usage: goyt-hls -url PLAYLIST_URL -out FILE.mp4 [-height 1080]",
		)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

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
				os.Stderr,
				"\rSegments downloaded: %d/%d",
				p.CompletedSegments,
				p.TotalSegments,
			)
		},
	)
	fmt.Fprintln(os.Stderr)

	if err != nil {
		return err
	}

	if result.CleanupError != nil {
		fmt.Fprintf(os.Stderr, "Cleanup warning: %v\n", result.CleanupError)
	}

	fmt.Println("Verifying output...")

	if _, err := verifier.VerifyMP4(
		ctx, result.Path, &result.PlaylistDuration,
	); err != nil {
		return fmt.Errorf("verification failed; output retained: %w", err)
	}

	if err := processor.CheckDecode(ctx, result.Path); err != nil {
		return fmt.Errorf("decode failed; output retained: %w", err)
	}

	fmt.Printf("Saved and verified %s\n", result.Path)
	return nil
}
