package goyt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StreamExtractor extracts media metadata for a given URL.
type StreamExtractor func(ctx context.Context, u *url.URL) (*Media, error)

// JobRunnerOptions contains operational dependencies and settings for executing a job.
type JobRunnerOptions struct {
	Extractor    StreamExtractor
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
	// If stagedPath is identical to destPath (unlikely, but safe guard)
	if stagedPath == destPath {
		return nil
	}

	// Try atomic rename first
	err := os.Rename(stagedPath, destPath)
	if err == nil {
		return nil
	}

	// If rename fails (e.g. across filesystems/mountpoints), stage safely on destination directory
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

	// Rename temp file in destination directory to destPath
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

	// Remove staged output file if present
	if m.PreCommit != nil && m.PreCommit.StagedRelativePath != "" {
		stagedPath := filepath.Join(jobDir, m.PreCommit.StagedRelativePath)
		_ = os.Remove(stagedPath)
	}
	_ = os.Remove(filepath.Join(jobDir, "staged_output.mp4"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.mkv"))
	_ = os.Remove(filepath.Join(jobDir, "staged_output.webm"))
}
