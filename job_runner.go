package goyt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StreamExtractor extracts media metadata for a given URL.
type StreamExtractor func(ctx context.Context, u *url.URL) (*Media, error)

// HLSExtractor extracts HLS manifest resource and media metadata for a given URL.
type HLSExtractor func(ctx context.Context, u *url.URL) (Resource, *Media, error)

// JobRunnerOptions contains operational dependencies and settings for executing a job.
type JobRunnerOptions struct {
	Extractor    StreamExtractor
	HLSExtractor HLSExtractor
	Downloader   *Downloader
	Processor    *FFmpeg
	Verifier     *Verifier
	Options      DownloadOptions
	URLRefreshes int
	Stdout       io.Writer
	Stderr       io.Writer
}

// ExecuteJob runs or resumes a persistent job in jobDir to completion.
func ExecuteJob(ctx context.Context, jobDir string, opts JobRunnerOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if opts.Downloader == nil {
		opts.Downloader = NewDownloader(nil)
	}
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}

	lock, err := AcquireJobLock(jobDir)
	if err != nil {
		return err
	}
	defer lock.Close()

	manifest, err := LoadJobManifest(jobDir)
	if err != nil {
		return err
	}

	// 1. Check if job is already completed or if destination was committed right before a crash.
	if manifest.Stage == JobStageCompleted {
		return verifyCompletedDestination(manifest, opts.Stdout)
	}

	if (manifest.Stage == JobStageReadyToCommit || manifest.PreCommit != nil) && manifest.PreCommit.FinalSHA256 != "" {
		if checkCommittedDestination(manifest) {
			manifest.Stage = JobStageCompleted
			now := time.Now().UTC()
			manifest.CompletedAt = &now
			cleanupIntermediateFiles(jobDir, manifest)
			_ = manifest.Save(jobDir)
			fmt.Fprintln(opts.Stdout, "Job already completed.")
			fmt.Fprintln(opts.Stdout, "Saved and verified:", manifest.DestinationPath)
			return nil
		}
	}

	if manifest.Transport == "hls" {
		return executeHLSJob(ctx, jobDir, manifest, opts)
	}
	return executeHTTPJob(ctx, jobDir, manifest, opts)
}

func executeHTTPJob(ctx context.Context, jobDir string, manifest *JobManifest, opts JobRunnerOptions) error {
	// 2. Validate integrity of existing completed input streams.
	needExtraction := false
	for i := range manifest.Streams {
		s := &manifest.Streams[i]
		absPath := filepath.Join(jobDir, s.RelativePath)
		if s.Completed {
			hash, size, err := ComputeFileSHA256(absPath)
			if err == nil && size == s.SizeBytes && strings.EqualFold(hash, s.SHA256) {
				fmt.Fprintf(opts.Stdout, "Reusing verified completed stream %d (format %s).\n", s.Index+1, s.Format.ID)
			} else {
				fmt.Fprintf(opts.Stderr, "Stream %d (format %s) integrity check failed or file missing; restarting download.\n", s.Index+1, s.Format.ID)
				s.Completed = false
				s.SizeBytes = 0
				s.SHA256 = ""
				_ = manifest.Save(jobDir)
				needExtraction = true
			}
		} else {
			needExtraction = true
		}
	}

	// 3. Re-extract fresh playback info if any stream is incomplete.
	currentResources := make([]Resource, len(manifest.Streams))
	if needExtraction {
		if opts.Extractor == nil {
			return errors.New("goyt: extractor is required to resume incomplete job")
		}

		u, err := url.Parse(manifest.SourceURL)
		if err != nil {
			return fmt.Errorf("invalid source URL %q: %w", manifest.SourceURL, err)
		}

		fmt.Fprintln(opts.Stdout, "Extracting fresh playback URLs for incomplete streams...")
		media, err := opts.Extractor(ctx, u)
		if err != nil {
			return err
		}
		if media.ID != manifest.VideoID {
			return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", media.ID, manifest.VideoID)
		}

		for i := range manifest.Streams {
			s := &manifest.Streams[i]
			if s.Completed {
				continue
			}
			origFormat := IdentityToFormat(s.Format, Resource{})
			matched, err := MatchRefreshedFormat(origFormat, media.Formats)
			if err != nil {
				return fmt.Errorf("%w: stream %d (%s): %v", ErrSelectionUnavailable, i+1, origFormat.ID, err)
			}
			currentResources[i] = matched.Resource
		}
	}

	refreshBudget := opts.URLRefreshes
	maxRefreshes := opts.URLRefreshes

	// 4. Download incomplete streams.
	if manifest.Stage == JobStagePlanned {
		manifest.Stage = JobStageDownloading
		if err := manifest.Save(jobDir); err != nil {
			return err
		}
	}

	for i := range manifest.Streams {
		if err := ctx.Err(); err != nil {
			return err
		}

		s := &manifest.Streams[i]
		if s.Completed {
			continue
		}

		absPath := filepath.Join(jobDir, s.RelativePath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0700); err != nil {
			return err
		}

		for {
			if err := ctx.Err(); err != nil {
				return err
			}

			res := currentResources[i]
			if res.ExpiresAt != nil && !res.ExpiresAt.After(time.Now()) {
				if refreshBudget <= 0 {
					return fmt.Errorf("stream %d URL has expired: %w", i+1, ErrResourceExpired)
				}
				refreshBudget--
				refreshCount := maxRefreshes - refreshBudget
				fmt.Fprintf(opts.Stderr, "Refreshing playback URLs after media access failure (%d/%d).\n", refreshCount, maxRefreshes)
				fmt.Fprintln(opts.Stderr, "Restarting the affected transfer.")

				u, _ := url.Parse(manifest.SourceURL)
				newMedia, extractErr := opts.Extractor(ctx, u)
				if extractErr != nil {
					return extractErr
				}
				if newMedia.ID != manifest.VideoID {
					return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newMedia.ID, manifest.VideoID)
				}
				origFormat := IdentityToFormat(s.Format, Resource{})
				matched, matchErr := MatchRefreshedFormat(origFormat, newMedia.Formats)
				if matchErr != nil {
					return matchErr
				}
				currentResources[i] = matched.Resource
				continue
			}

			// Check if resuming partial file or starting fresh
			partFile := absPath + ".part"
			partStateFile := partFile + ".json"
			if info, err := os.Stat(partFile); err == nil && info.Size() > 0 {
				if _, errState := os.Stat(partStateFile); errState == nil {
					fmt.Fprintf(opts.Stdout, "Resuming download for stream %d (format %s)...\n", i+1, s.Format.ID)
				} else {
					fmt.Fprintf(opts.Stdout, "Downloading stream %d (format %s)...\n", i+1, s.Format.ID)
				}
			} else {
				fmt.Fprintf(opts.Stdout, "Downloading stream %d (format %s)...\n", i+1, s.Format.ID)
			}

			dlRes, dlErr := opts.Downloader.Download(
				ctx,
				currentResources[i],
				absPath,
				DownloadOptions{
					Resume:       true,
					MaxRetries:   opts.Options.MaxRetries,
					RetryDelay:   opts.Options.RetryDelay,
					StallTimeout: opts.Options.StallTimeout,
					OnProgress: func(p Progress) {
						if p.TotalBytes > 0 {
							fmt.Fprintf(
								opts.Stderr,
								"\rFormat %s: %.2f / %.2f MiB",
								s.Format.ID,
								float64(p.DownloadedBytes)/(1024*1024),
								float64(p.TotalBytes)/(1024*1024),
							)
						} else {
							fmt.Fprintf(
								opts.Stderr,
								"\rFormat %s: %.2f MiB downloaded",
								s.Format.ID,
								float64(p.DownloadedBytes)/(1024*1024),
							)
						}
					},
					OnRetry: func(diag RetryDiagnostic) {
						fmt.Fprintln(opts.Stderr)
						if diag.Resumed {
							fmt.Fprintf(
								opts.Stderr,
								"Stream %d (format %s): recovery attempt %d/%d after error (%s). Resuming from %.2f MiB (HTTP 206 validated).\n",
								i+1,
								s.Format.ID,
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
								opts.Stderr,
								"Stream %d (format %s): recovery attempt %d/%d after error (%s). Restarting from byte 0 (%s; discarded %.2f MiB partial data).\n",
								i+1,
								s.Format.ID,
								diag.Attempt,
								diag.MaxRetries,
								diag.Reason,
								actionReason,
								float64(diag.PriorOffset)/(1024*1024),
							)
						}
						if opts.Options.OnRetry != nil {
							opts.Options.OnRetry(diag)
						}
					},
				},
			)
			fmt.Fprintln(opts.Stderr)

			if dlErr == nil {
				hash, size, hErr := ComputeFileSHA256(dlRes.Path)
				if hErr != nil {
					return fmt.Errorf("compute stream SHA256: %w", hErr)
				}
				s.Completed = true
				s.SizeBytes = size
				s.SHA256 = hash
				if err := manifest.Save(jobDir); err != nil {
					return err
				}
				break
			}

			if ctx.Err() != nil || errors.Is(dlErr, context.Canceled) || errors.Is(dlErr, context.DeadlineExceeded) {
				return dlErr
			}

			if IsRefreshTrigger(dlErr) && refreshBudget > 0 {
				refreshBudget--
				refreshCount := maxRefreshes - refreshBudget
				fmt.Fprintf(opts.Stderr, "Refreshing playback URLs after media access failure (%d/%d).\n", refreshCount, maxRefreshes)
				fmt.Fprintln(opts.Stderr, "Restarting the affected transfer.")

				u, _ := url.Parse(manifest.SourceURL)
				newMedia, extractErr := opts.Extractor(ctx, u)
				if extractErr != nil {
					return extractErr
				}
				if newMedia.ID != manifest.VideoID {
					return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newMedia.ID, manifest.VideoID)
				}
				origFormat := IdentityToFormat(s.Format, Resource{})
				matched, matchErr := MatchRefreshedFormat(origFormat, newMedia.Formats)
				if matchErr != nil {
					return matchErr
				}
				currentResources[i] = matched.Resource
				continue
			}

			return dlErr
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// 5. Processing / Merging
	manifest.Stage = JobStageProcessing
	if err := manifest.Save(jobDir); err != nil {
		return err
	}

	var stagedPath string
	if manifest.Mode == "audio-only" {
		if opts.Processor == nil {
			return errors.New("goyt: audio processing requires an FFmpeg processor")
		}

		spec := manifest.ToAudioOutputSpec()
		stagedPath = filepath.Join(jobDir, "staged_output"+spec.Extension)
		_ = os.Remove(stagedPath)

		inputPath := filepath.Join(jobDir, manifest.Streams[0].RelativePath)
		fmt.Fprintln(opts.Stdout, "Processing audio output...")
		if err := opts.Processor.ConvertAudio(ctx, inputPath, stagedPath, spec, false); err != nil {
			return fmt.Errorf("audio processing failed: %w", err)
		}
	} else {
		// Video mode
		outExt := "." + normalizeContainer(manifest.OutputContainer)
		stagedPath = filepath.Join(jobDir, "staged_output"+outExt)
		_ = os.Remove(stagedPath)

		if manifest.NeedsMerge {
			if opts.Processor == nil {
				return errors.New("goyt: stream merging requires an FFmpeg processor")
			}
			input0 := filepath.Join(jobDir, manifest.Streams[0].RelativePath)
			input1 := filepath.Join(jobDir, manifest.Streams[1].RelativePath)
			fmt.Fprintln(opts.Stdout, "Merging video and audio streams...")
			if err := opts.Processor.Merge(ctx, input0, input1, stagedPath); err != nil {
				return fmt.Errorf("stream merging failed: %w", err)
			}
		} else if manifest.NeedsRemux {
			if opts.Processor == nil {
				return errors.New("goyt: stream remuxing requires an FFmpeg processor")
			}
			input0 := filepath.Join(jobDir, manifest.Streams[0].RelativePath)
			fmt.Fprintln(opts.Stdout, "Remuxing stream container...")
			if err := opts.Processor.Remux(ctx, input0, stagedPath); err != nil {
				return fmt.Errorf("stream remuxing failed: %w", err)
			}
		} else {
			// Single direct stream without remux
			input0 := filepath.Join(jobDir, manifest.Streams[0].RelativePath)
			stagedPath = input0
		}
	}

	return verifyAndCommitJob(ctx, jobDir, manifest, stagedPath, opts)
}

func executeHLSJob(ctx context.Context, jobDir string, manifest *JobManifest, opts JobRunnerOptions) error {
	if manifest.HLS == nil || len(manifest.HLS.Tracks) == 0 {
		return fmt.Errorf("%w: missing HLS state", ErrInvalidManifest)
	}

	// 1. Validate integrity of all completed segments across all tracks.
	needDownload := false
	totalSegments := 0
	reusedSegments := 0

	for i := range manifest.HLS.Tracks {
		track := &manifest.HLS.Tracks[i]
		totalSegments += len(track.Segments)
		for j := range track.Segments {
			seg := &track.Segments[j]
			if seg.Completed {
				absPath := filepath.Join(jobDir, seg.RelativePath)
				hash, size, err := ComputeFileSHA256(absPath)
				if err == nil && size == seg.SizeBytes && strings.EqualFold(hash, seg.SHA256) {
					reusedSegments++
				} else {
					seg.Completed = false
					seg.SizeBytes = 0
					seg.SHA256 = ""
					needDownload = true
				}
			} else {
				needDownload = true
			}
		}
	}

	// 2. If all segments are already completed and verified locally:
	// We can proceed directly to local processing without any network extraction!
	if !needDownload && reusedSegments == totalSegments && totalSegments > 0 {
		fmt.Fprintf(opts.Stdout, "Reusing all %d verified segments locally.\n", totalSegments)
		return processHLSOutput(ctx, jobDir, manifest, opts)
	}

	// 3. Incomplete job needs fresh HLS manifest extraction.
	if opts.HLSExtractor == nil {
		return errors.New("goyt: HLS extractor is required to resume incomplete HLS job")
	}

	u, err := url.Parse(manifest.SourceURL)
	if err != nil {
		return fmt.Errorf("invalid source URL %q: %w", manifest.SourceURL, err)
	}

	fmt.Fprintln(opts.Stdout, "Extracting fresh HLS playback URLs for incomplete job...")
	manifestRes, media, err := opts.HLSExtractor(ctx, u)
	if err != nil {
		return err
	}
	if media.ID != manifest.VideoID {
		return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", media.ID, manifest.VideoID)
	}

	hlsDownloader, err := NewHLSDownloader(opts.Downloader.client, opts.Processor)
	if err != nil {
		return err
	}

	// Resolve refreshed presentation
	resolvedTracks, restartReason, err := resolveRefreshedHLSTracks(ctx, hlsDownloader, manifest, manifestRes)
	if err != nil {
		return err
	}

	// 4. Compare playlist structure & URL fingerprints for conservative reuse
	restartGeneration := false
	if restartReason != "" {
		restartGeneration = true
	} else {
		if len(resolvedTracks) != len(manifest.HLS.Tracks) {
			restartGeneration = true
			restartReason = "track count changed"
		} else {
			for i := range manifest.HLS.Tracks {
				savedTrack := &manifest.HLS.Tracks[i]
				freshTrack := resolvedTracks[i]
				if len(freshTrack.Playlist.Segments) != len(savedTrack.Segments) {
					restartGeneration = true
					restartReason = fmt.Sprintf("track %d segment count changed (%d -> %d)", i, len(savedTrack.Segments), len(freshTrack.Playlist.Segments))
					break
				}
				for j := range savedTrack.Segments {
					savedSeg := savedTrack.Segments[j]
					freshSeg := freshTrack.Playlist.Segments[j]
					if math.Abs(savedSeg.DurationSeconds-freshSeg.Duration.Seconds()) > 0.0001 {
						restartGeneration = true
						restartReason = fmt.Sprintf("track %d segment %d duration changed", i, j)
						break
					}
					freshFingerprint := ComputeURLFingerprint(freshSeg.URL)
					if freshFingerprint != savedSeg.URLFingerprint {
						restartGeneration = true
						restartReason = "segment URLs or tokens changed"
						break
					}
				}
				if restartGeneration {
					break
				}
			}
		}
	}

	if restartGeneration {
		newGen := manifest.HLS.Generation + 1
		fmt.Fprintf(opts.Stderr, "HLS presentation restart (generation %d): %s. Restarting download.\n", newGen, restartReason)
		manifest.HLS.Generation = newGen
		rebuildHLSTracks(manifest.HLS, resolvedTracks, newGen)
		if err := manifest.Save(jobDir); err != nil {
			return err
		}
		reusedSegments = 0
	} else {
		if reusedSegments > 0 {
			fmt.Fprintf(opts.Stdout, "Reusing %d completed segments; %d remaining to download.\n", reusedSegments, totalSegments-reusedSegments)
		}
	}

	// 5. Download missing segments with checkpointing & URL refresh handling
	if manifest.Stage == JobStagePlanned {
		manifest.Stage = JobStageDownloading
		if err := manifest.Save(jobDir); err != nil {
			return err
		}
	}

	refreshBudget := opts.URLRefreshes
	maxRefreshes := opts.URLRefreshes

downloadLoop:
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		totSegs := 0
		compSegs := 0
		for i := range manifest.HLS.Tracks {
			totSegs += len(manifest.HLS.Tracks[i].Segments)
			for j := range manifest.HLS.Tracks[i].Segments {
				if manifest.HLS.Tracks[i].Segments[j].Completed {
					compSegs++
				}
			}
		}

		for i := range manifest.HLS.Tracks {
			trackState := &manifest.HLS.Tracks[i]
			freshTrack := resolvedTracks[i]
			trackDir := filepath.Join(jobDir, "hls", fmt.Sprintf("gen-%d", manifest.HLS.Generation), fmt.Sprintf("track-%d", i))
			if err := os.MkdirAll(trackDir, 0700); err != nil {
				return err
			}

			for j := range trackState.Segments {
				if err := ctx.Err(); err != nil {
					return err
				}

				segState := &trackState.Segments[j]
				if segState.Completed {
					continue
				}

				freshSeg := freshTrack.Playlist.Segments[j]
				downloadPath := filepath.Join(trackDir, fmt.Sprintf("segment-%05d.download", j))
				_ = os.Remove(downloadPath)

				headers := freshTrack.Headers.Clone()
				segmentURL, parseErr := url.Parse(freshSeg.URL)
				if parseErr != nil || !validHLSURL(segmentURL) {
					return fmt.Errorf("goyt: segment %d has an invalid URL", j+1)
				}
				if origin(freshTrack.BaseURL) != origin(segmentURL) {
					headers = nil
				}

				_, dlErr := opts.Downloader.Download(
					ctx,
					Resource{URL: freshSeg.URL, Headers: headers},
					downloadPath,
					DownloadOptions{
						Resume:       false,
						MaxRetries:   opts.Options.MaxRetries,
						RetryDelay:   opts.Options.RetryDelay,
						StallTimeout: opts.Options.StallTimeout,
					},
				)

				if dlErr != nil {
					if ctx.Err() != nil || errors.Is(dlErr, context.Canceled) || errors.Is(dlErr, context.DeadlineExceeded) {
						return dlErr
					}

					if IsRefreshTrigger(dlErr) && refreshBudget > 0 {
						refreshBudget--
						refreshCount := maxRefreshes - refreshBudget
						fmt.Fprintf(opts.Stderr, "Refreshing playback URLs after media access failure (%d/%d).\n", refreshCount, maxRefreshes)
						fmt.Fprintln(opts.Stderr, "Restarting the affected transfer.")

						u, _ := url.Parse(manifest.SourceURL)
						newManifestRes, newMedia, extErr := opts.HLSExtractor(ctx, u)
						if extErr != nil {
							return extErr
						}
						if newMedia.ID != manifest.VideoID {
							return fmt.Errorf("goyt: refreshed video ID %q does not match original %q", newMedia.ID, manifest.VideoID)
						}

						newResolvedTracks, _, resErr := resolveRefreshedHLSTracks(ctx, hlsDownloader, manifest, newManifestRes)
						if resErr != nil {
							return resErr
						}

						newGen := manifest.HLS.Generation + 1
						fmt.Fprintf(opts.Stderr, "HLS presentation restart (generation %d): URL refresh. Restarting download.\n", newGen)
						manifest.HLS.Generation = newGen
						rebuildHLSTracks(manifest.HLS, newResolvedTracks, newGen)
						if err := manifest.Save(jobDir); err != nil {
							return err
						}
						resolvedTracks = newResolvedTracks
						continue downloadLoop
					}

					return fmt.Errorf("segment %d: %w", j+1, dlErr)
				}

				// Inspect framing
				ext, inspErr := inspectHLSSegment(ctx, downloadPath)
				if inspErr != nil {
					_ = os.Remove(downloadPath)
					return fmt.Errorf("segment %d: %w", j+1, inspErr)
				}

				localName := fmt.Sprintf("segment-%05d%s", j, ext)
				localPath := filepath.Join(trackDir, localName)
				relPath := filepath.Join("hls", fmt.Sprintf("gen-%d", manifest.HLS.Generation), fmt.Sprintf("track-%d", i), localName)

				if err := os.Rename(downloadPath, localPath); err != nil {
					return err
				}

				hash, size, hErr := ComputeFileSHA256(localPath)
				if hErr != nil {
					return fmt.Errorf("compute segment SHA256: %w", hErr)
				}

				segState.Completed = true
				segState.RelativePath = relPath
				segState.SizeBytes = size
				segState.SHA256 = hash

				if err := manifest.Save(jobDir); err != nil {
					return err
				}

				compSegs++
				fmt.Fprintf(opts.Stderr, "\rTrack %d: segment %d/%d (total %d/%d)", i+1, j+1, len(trackState.Segments), compSegs, totSegs)
			}
		}
		fmt.Fprintln(opts.Stderr)
		break downloadLoop
	}

	return processHLSOutput(ctx, jobDir, manifest, opts)
}

func resolveRefreshedHLSTracks(
	ctx context.Context,
	hlsDownloader *HLSDownloader,
	manifest *JobManifest,
	manifestRes Resource,
) ([]*HLSTrack, string, error) {
	if manifest.Mode == "audio-only" {
		if manifest.HLS.SelectedAudio != nil {
			origAudio := IdentityToAudio(*manifest.HLS.SelectedAudio)
			track, newAudio, err := hlsDownloader.ResolveRefreshedAudioOnlyMaster(ctx, manifestRes, origAudio)
			if err != nil {
				return nil, "", fmt.Errorf("%w: %v", ErrSelectionUnavailable, err)
			}
			manifest.HLS.SelectedAudio = &HLSAudioIdentity{
				GroupID:    newAudio.GroupID,
				Name:       newAudio.Name,
				Language:   newAudio.Language,
				Default:    newAudio.Default,
				AutoSelect: newAudio.AutoSelect,
			}
			return []*HLSTrack{track}, "", nil
		}
		track, err := hlsDownloader.ResolveMediaPlaylist(ctx, manifestRes)
		if err != nil {
			return nil, "", err
		}
		return []*HLSTrack{track}, "", nil
	}

	// Video mode
	if manifest.HLS.SelectedVariant != nil {
		origVariant := IdentityToVariant(*manifest.HLS.SelectedVariant)
		var origAudio *HLSAudioRendition
		if manifest.HLS.SelectedAudio != nil {
			a := IdentityToAudio(*manifest.HLS.SelectedAudio)
			origAudio = &a
		}
		resolved, err := hlsDownloader.ResolveRefreshedMaster(ctx, manifestRes, origVariant, origAudio)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrSelectionUnavailable, err)
		}
		if resolved.SelectedVariant != nil {
			v := VariantToIdentity(*resolved.SelectedVariant)
			manifest.HLS.SelectedVariant = &v
		}
		if resolved.SelectedAudio != nil {
			a := AudioToIdentity(*resolved.SelectedAudio)
			manifest.HLS.SelectedAudio = &a
		}
		tracks := []*HLSTrack{resolved.Video}
		if resolved.Audio != nil {
			tracks = append(tracks, resolved.Audio)
		}
		return tracks, "", nil
	}

	// Single media playlist
	track, err := hlsDownloader.ResolveMediaPlaylist(ctx, manifestRes)
	if err != nil {
		return nil, "", err
	}
	return []*HLSTrack{track}, "", nil
}

func rebuildHLSTracks(hlsState *JobHLSState, resolvedTracks []*HLSTrack, gen int) {
	tracks := make([]HLSTrackState, len(resolvedTracks))
	for i, t := range resolvedTracks {
		role := "video"
		if len(resolvedTracks) == 1 && len(hlsState.Tracks) > 0 && hlsState.Tracks[0].Role == "combined" {
			role = "combined"
		} else if i == 1 || (len(resolvedTracks) == 1 && len(hlsState.Tracks) > 0 && hlsState.Tracks[0].Role == "audio") {
			role = "audio"
		}

		var variantID *HLSVariantIdentity
		if role == "video" || role == "combined" {
			variantID = hlsState.SelectedVariant
		}
		var audioID *HLSAudioIdentity
		if role == "audio" {
			audioID = hlsState.SelectedAudio
		}

		segs := make([]HLSSegmentState, len(t.Playlist.Segments))
		targetDuration := 1
		for j, s := range t.Playlist.Segments {
			segs[j] = HLSSegmentState{
				Index:           j,
				DurationSeconds: s.Duration.Seconds(),
				URLFingerprint:  ComputeURLFingerprint(s.URL),
				RelativePath:    filepath.Join("hls", fmt.Sprintf("gen-%d", gen), fmt.Sprintf("track-%d", i), fmt.Sprintf("segment-%05d", j)),
				Completed:       false,
			}
			sec := int(math.Ceil(s.Duration.Seconds()))
			if sec > targetDuration {
				targetDuration = sec
			}
		}

		tracks[i] = HLSTrackState{
			Index:           i,
			Role:            role,
			Generation:      gen,
			Variant:         variantID,
			Audio:           audioID,
			TargetDuration:  targetDuration,
			DurationSeconds: t.Playlist.Duration.Seconds(),
			Segments:        segs,
		}
	}
	hlsState.Tracks = tracks
}

func processHLSOutput(ctx context.Context, jobDir string, manifest *JobManifest, opts JobRunnerOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	manifest.Stage = JobStageProcessing
	if err := manifest.Save(jobDir); err != nil {
		return err
	}

	gen := manifest.HLS.Generation
	genDir := filepath.Join(jobDir, "hls", fmt.Sprintf("gen-%d", gen))

	// Write local media playlists for each track
	for i, track := range manifest.HLS.Tracks {
		var sb strings.Builder
		fmt.Fprintf(&sb, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-PLAYLIST-TYPE:VOD\n",
			track.TargetDuration, track.MediaSequence)
		for _, seg := range track.Segments {
			relToGen := filepath.Join(fmt.Sprintf("track-%d", i), filepath.Base(seg.RelativePath))
			fmt.Fprintf(&sb, "#EXTINF:%.9f,\n%s\n", seg.DurationSeconds, relToGen)
		}
		sb.WriteString("#EXT-X-ENDLIST\n")

		trackM3U8 := filepath.Join(genDir, fmt.Sprintf("track-%d.m3u8", i))
		if err := os.WriteFile(trackM3U8, []byte(sb.String()), 0600); err != nil {
			return fmt.Errorf("write track playlist: %w", err)
		}
	}

	var localInput string
	if len(manifest.HLS.Tracks) == 2 {
		localInput = filepath.Join(genDir, "master.m3u8")
		master := "#EXTM3U\n" +
			"#EXT-X-VERSION:3\n" +
			"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\"," +
			"NAME=\"Audio\",DEFAULT=YES,AUTOSELECT=YES," +
			"URI=\"track-1.m3u8\"\n" +
			"#EXT-X-STREAM-INF:BANDWIDTH=1000000,AUDIO=\"audio\"\n" +
			"track-0.m3u8\n"
		if err := os.WriteFile(localInput, []byte(master), 0600); err != nil {
			return fmt.Errorf("write master playlist: %w", err)
		}
	} else {
		localInput = filepath.Join(genDir, "track-0.m3u8")
	}

	var stagedPath string
	if manifest.Mode == "audio-only" {
		if opts.Processor == nil {
			return errors.New("goyt: audio processing requires an FFmpeg processor")
		}
		spec := manifest.ToAudioOutputSpec()
		stagedPath = filepath.Join(jobDir, "staged_output"+spec.Extension)
		_ = os.Remove(stagedPath)

		fmt.Fprintln(opts.Stdout, "Processing audio output...")
		if err := opts.Processor.ConvertAudio(ctx, localInput, stagedPath, spec, true); err != nil {
			return fmt.Errorf("audio processing failed: %w", err)
		}
	} else {
		outExt := "." + normalizeContainer(manifest.OutputContainer)
		stagedPath = filepath.Join(jobDir, "staged_output"+outExt)
		_ = os.Remove(stagedPath)

		fmt.Fprintln(opts.Stdout, "Processing HLS video output...")
		var mapArgs []string
		if len(manifest.HLS.Tracks) == 2 {
			mapArgs = []string{"-map", "0:v:0", "-map", "0:a:0"}
		} else {
			mapArgs = []string{"-map", "0:v:0", "-map", "0:a:0?"}
		}
		if err := opts.Processor.process(ctx, "local-hls", []string{localInput}, mapArgs, stagedPath); err != nil {
			return fmt.Errorf("video processing failed: %w", err)
		}
	}

	return verifyAndCommitJob(ctx, jobDir, manifest, stagedPath, opts)
}

func verifyAndCommitJob(ctx context.Context, jobDir string, manifest *JobManifest, stagedPath string, opts JobRunnerOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// 6. Verification
	manifest.Stage = JobStageVerifying
	if err := manifest.Save(jobDir); err != nil {
		return err
	}

	var expectedDuration *time.Duration
	if manifest.DurationSeconds != nil {
		d := time.Duration(*manifest.DurationSeconds * float64(time.Second))
		expectedDuration = &d
	}

	if opts.Verifier == nil {
		v, err := NewVerifier("")
		if err != nil {
			return err
		}
		opts.Verifier = v
	}

	if manifest.Mode == "audio-only" {
		spec := manifest.ToAudioOutputSpec()
		fmt.Fprintln(opts.Stdout, "Verifying audio output streams and duration...")
		if _, err := opts.Verifier.VerifyAudio(ctx, stagedPath, spec, expectedDuration); err != nil {
			return fmt.Errorf("verification failed; output retained: %w", err)
		}
		if manifest.DecodeCheck {
			fmt.Fprintln(opts.Stdout, "Decoding the complete audio output...")
			if err := opts.Processor.CheckDecodeAudio(ctx, stagedPath); err != nil {
				return fmt.Errorf("decode failed; output retained: %w", err)
			}
			fmt.Fprintln(opts.Stdout, "Full audio decode check passed.")
		}
	} else {
		fmt.Fprintln(opts.Stdout, "Verifying output streams and duration...")
		if _, err := opts.Verifier.VerifyMP4(ctx, stagedPath, expectedDuration); err != nil {
			return fmt.Errorf("verification failed; output retained: %w", err)
		}
		if manifest.DecodeCheck {
			fmt.Fprintln(opts.Stdout, "Decoding the complete output...")
			if err := opts.Processor.CheckDecode(ctx, stagedPath); err != nil {
				return fmt.Errorf("decode failed; output retained: %w", err)
			}
			fmt.Fprintln(opts.Stdout, "Full decode check passed.")
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// 7. Ready To Commit
	finalHash, finalSize, err := ComputeFileSHA256(stagedPath)
	if err != nil {
		return fmt.Errorf("compute final output SHA256: %w", err)
	}

	manifest.PreCommit = &JobPreCommit{
		StagedRelativePath: filepath.Base(stagedPath),
		FinalSizeBytes:     finalSize,
		FinalSHA256:        finalHash,
	}
	manifest.Stage = JobStageReadyToCommit
	if err := manifest.Save(jobDir); err != nil {
		return err
	}

	// 8. Commit to Destination
	destPath := manifest.DestinationPath
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	if err := commitStagedFile(stagedPath, destPath); err != nil {
		return fmt.Errorf("failed to commit destination file: %w", err)
	}

	// 9. Completed
	manifest.Stage = JobStageCompleted
	now := time.Now().UTC()
	manifest.CompletedAt = &now
	cleanupIntermediateFiles(jobDir, manifest)
	_ = manifest.Save(jobDir)

	fmt.Fprintln(opts.Stdout, "Saved and verified:", destPath)
	return nil
}

// ToAudioOutputSpec converts the manifest's AudioOutputSpec into a runtime AudioOutputSpec.
func (m *JobManifest) ToAudioOutputSpec() AudioOutputSpec {
	if m.AudioOutputSpec == nil {
		return AudioOutputSpec{}
	}
	return AudioOutputSpec{
		RequestedFormat: m.AudioOutputSpec.RequestedFormat,
		ResolvedCodec:   m.AudioOutputSpec.ResolvedCodec,
		Container:       m.AudioOutputSpec.Container,
		Extension:       m.AudioOutputSpec.Extension,
		Copy:            m.AudioOutputSpec.Copy,
		Encoder:         m.AudioOutputSpec.Encoder,
		Quality:         m.AudioOutputSpec.Quality,
		Bitrate:         m.AudioOutputSpec.Bitrate,
	}
}

func checkCommittedDestination(m *JobManifest) bool {
	if m == nil || m.PreCommit == nil || m.PreCommit.FinalSHA256 == "" {
		return false
	}
	info, err := os.Stat(m.DestinationPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != m.PreCommit.FinalSizeBytes {
		return false
	}
	hash, _, err := ComputeFileSHA256(m.DestinationPath)
	if err != nil {
		return false
	}
	return strings.EqualFold(hash, m.PreCommit.FinalSHA256)
}

func verifyCompletedDestination(m *JobManifest, stdout io.Writer) error {
	if checkCommittedDestination(m) {
		fmt.Fprintln(stdout, "Job already completed.")
		fmt.Fprintln(stdout, "Saved and verified:", m.DestinationPath)
		return nil
	}
	return fmt.Errorf("%w: destination file %q was removed or modified; start a new job to re-download", ErrCompletedOutputMismatch, m.DestinationPath)
}

func commitStagedFile(stagedPath, destPath string) error {
	if stagedPath == destPath {
		return nil
	}

	err := os.Rename(stagedPath, destPath)
	if err == nil {
		return nil
	}

	destDir := filepath.Dir(destPath)
	tmpFile, err := os.CreateTemp(destDir, ".goyt-dest-stage-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	srcFile, err := os.Open(stagedPath)
	if err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	_, err = io.Copy(tmpFile, srcFile)
	srcFile.Close()
	if err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	_ = os.Remove(stagedPath)
	return nil
}

func cleanupIntermediateFiles(jobDir string, m *JobManifest) {
	// Remove owned inputs directory
	inputsDir := filepath.Join(jobDir, "inputs")
	_ = os.RemoveAll(inputsDir)

	// Remove owned hls directory
	hlsDir := filepath.Join(jobDir, "hls")
	_ = os.RemoveAll(hlsDir)

	// Remove staged output file if present
	if m.PreCommit != nil && m.PreCommit.StagedRelativePath != "" {
		stagedPath := filepath.Join(jobDir, m.PreCommit.StagedRelativePath)
		_ = os.Remove(stagedPath)
	}
	_ = os.Remove(filepath.Join(jobDir, "staged_output.mp4"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.mkv"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.webm"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.mp3"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.aac"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.m4a"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.flac"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.alac"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.opus"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.ogg"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.wav"))
}
