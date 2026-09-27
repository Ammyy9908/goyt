package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"text/tabwriter"
	"time"

	"github.com/ammyy9908/goyt/extractor/youtube"
)

func runInspect(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	source := flags.String("url", "", "public YouTube video URL")
	clientName := flags.String(
		"client",
		"all",
		"response to inspect: web, visionos, or all",
	)
	jsonOutput := flags.Bool(
		"json",
		false,
		"emit machine-readable JSON output",
	)

	flags.Usage = func() {
		fmt.Fprintln(stderr, `Usage: goyt inspect -url YOUTUBE_URL [options]
  goyt inspect -url YOUTUBE_URL [-client web|visionos|all] [-json]
  goyt inspect -help`)
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return err
	}

	if *source == "" || flags.NArg() != 0 {
		flags.Usage()
		return errors.New("usage: goyt inspect -url YOUTUBE_URL [-client web|visionos|all] [-json]")
	}

	switch *clientName {
	case "web", "visionos", "all":
	default:
		return errors.New("client must be web, visionos, or all")
	}

	u, err := url.Parse(*source)
	if err != nil {
		return errors.New("invalid URL")
	}

	extractor := youtube.New(nil)
	if !extractor.Match(u) {
		return errors.New("unsupported YouTube video URL")
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	clients := []string{*clientName}
	if *clientName == "all" {
		clients = []string{"web", "visionos"}
	}

	var results []youtube.ClientInspectResult
	var failures []error

	for _, name := range clients {
		var report *youtube.Report

		if name == "web" {
			report, err = extractor.Inspect(ctx, u)
		} else {
			report, err = extractor.InspectVisionOS(ctx, u)
		}

		if *jsonOutput {
			results = append(results, youtube.BuildClientInspectResult(name, report, err))
			if err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", name, err))
			}
		} else {
			fmt.Fprintf(stdout, "\n=== %s ===\n", name)

			if err != nil {
				fmt.Fprintf(stdout, "Inspection failed: %v\n", err)
				failures = append(failures, fmt.Errorf("%s: %w", name, err))
				continue
			}

			if err := printInspectReport(report, stdout); err != nil {
				return err
			}
		}
	}

	if *jsonOutput {
		resp := youtube.BuildInspectResponse(results)
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal inspect JSON: %w", err)
		}
		data = append(data, '\n')
		if _, err := stdout.Write(data); err != nil {
			return fmt.Errorf("write inspect JSON: %w", err)
		}
	}

	return errors.Join(failures...)
}

func printInspectReport(report *youtube.Report, w io.Writer) error {
	fmt.Fprintf(w, "ID: %s\n", report.Media.ID)
	fmt.Fprintf(w, "Title: %s\n", report.Media.Title)
	fmt.Fprintf(w, "Playback status: %s\n", report.PlaybackStatus)

	if report.PlaybackReason != "" {
		fmt.Fprintf(w, "Playback reason: %s\n", report.PlaybackReason)
	}

	if report.Media.Duration != nil {
		fmt.Fprintf(w, "Duration: %s\n", *report.Media.Duration)
	}

	fmt.Fprintf(
		w,
		"Streaming endpoints: HLS=%t DASH=%t SABR=%t\n\n",
		report.HasHLSManifest,
		report.HasDASHManifest,
		report.HasSABREndpoint,
	)

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(
		tw,
		"ID\tQUALITY\tDIRECT URL\tSIG CHALLENGE\tN CHALLENGE\tDRM REPORTED\tMIME",
	)

	for _, f := range report.Formats {
		fmt.Fprintf(
			tw,
			"%d\t%s\t%t\t%t\t%t\t%t\t%s\n",
			f.ID,
			f.Quality,
			f.HasDirectURL,
			f.SignatureChallenge,
			f.NChallenge,
			f.DRMReported,
			f.MIMEType,
		)
	}

	if err := tw.Flush(); err != nil {
		return err
	}

	if len(report.AudioTracks) > 0 {
		fmt.Fprintln(w, "\nAudio tracks (Player API):")
		aw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(aw, "ID\tNAME\tDEFAULT\tORIGINAL")
		for _, at := range report.AudioTracks {
			fmt.Fprintf(
				aw,
				"%s\t%s\t%t\t%t\n",
				at.ID,
				at.DisplayName,
				at.AudioIsDefault,
				at.IsOriginal,
			)
		}
		if err := aw.Flush(); err != nil {
			return err
		}
	}

	if len(report.HLSRenditions) > 0 {
		fmt.Fprintln(w, "\nHLS audio renditions:")
		hw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(hw, "GROUP-ID\tNAME\tLANGUAGE\tDEFAULT\tAUTOSELECT\tORIGINAL")
		for _, hr := range report.HLSRenditions {
			fmt.Fprintf(
				hw,
				"%s\t%s\t%s\t%t\t%t\t%t\n",
				hr.GroupID,
				hr.Name,
				hr.Language,
				hr.Default,
				hr.AutoSelect,
				hr.IsOriginal,
			)
		}
		if err := hw.Flush(); err != nil {
			return err
		}
	}

	fmt.Fprintln(w, "\nLimitations:")
	for _, limitation := range report.Limitations {
		fmt.Fprintln(w, "-", limitation)
	}

	return nil
}
