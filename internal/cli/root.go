package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ammyy9908/goyt"
)

// Run routes CLI execution to the appropriate subcommand or legacy shorthand.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("usage: goyt <command> [flags]")
	}

	switch args[0] {
	case "-version", "--version", "version":
		fmt.Fprintln(stdout, goyt.Version)
		return nil

	case "-help", "--help", "-h", "help":
		printUsage(stdout)
		return nil

	case "download":
		return runDownload(ctx, args[1:], stdout, stderr)

	case "inspect":
		return runInspect(ctx, args[1:], stdout, stderr)

	case "hls":
		return runHLS(ctx, args[1:], stdout, stderr)

	default:
		// Legacy shorthand: `goyt -url ...` routes directly to `goyt download`.
		if strings.HasPrefix(args[0], "-") {
			return runDownload(ctx, args, stdout, stderr)
		}

		fmt.Fprintf(stderr, "goyt: unknown command %q\n", args[0])
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  goyt download -url URL [-client visionos|web] [-js-runtime none|node|deno|bun|qjs|auto|PATH] [-transport http|hls] [-video-codec h264|vp9|av1] [-container mp4|webm|mkv] [-height 1080] [-audio-language LANG] [-timeout 30m] [-stall-timeout 60s] [-url-refreshes 1] [-job-dir DIR] [-out video.mp4] [-decode-check]
  goyt download -url URL -audio-only [-client visionos|web] [-js-runtime none|node|deno|bun|qjs|auto|PATH] [-audio-format best|aac|alac|flac|m4a|mp3|opus|vorbis|wav] [-audio-quality 0-9] [-audio-bitrate BITRATE] [-transport http|hls] [-audio-language LANG] [-timeout 30m] [-stall-timeout 60s] [-url-refreshes 1] [-job-dir DIR] [-out audio.<ext>] [-decode-check]
  goyt download -resume-job DIR [-js-runtime none|node|deno|bun|qjs|auto|PATH] [-timeout 30m] [-stall-timeout 60s] [-url-refreshes 1]
  goyt inspect -url URL [-client web|visionos|all] [-js-runtime none|node|deno|bun|qjs|auto|PATH] [-json]
  goyt hls -url PLAYLIST_URL [-height 1080] [-timeout 30m] [-stall-timeout 60s] [-out hls.mp4]
  goyt -version
  goyt -help

Commands:
  download    Download a YouTube video via direct HTTP or HLS
  inspect     Inspect YouTube formats, streaming endpoints, and client responses
  hls         Download an arbitrary HLS master or media playlist

Legacy shorthand:
  goyt -url URL ... is supported as an alias for goyt download.

Use "goyt <command> -help" for more information about a command.`)
}
