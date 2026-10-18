package youtube

import (
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ammyy9908/goyt"
)

// ExtractionStage represents an identified milestone in the YouTube extraction lifecycle.
type ExtractionStage string

const (
	StageWatchPageFetch   ExtractionStage = "watch_page_fetch"
	StageWatchPageParse   ExtractionStage = "watch_page_parse"
	StageVisitorDataParse ExtractionStage = "visitor_data_parse"
	StagePlayerRequest    ExtractionStage = "player_request"
	StagePlayerParse      ExtractionStage = "player_parse"
	StagePOTokenAcquire   ExtractionStage = "po_token_acquire"
	StageChallengeSolve   ExtractionStage = "challenge_solve"
	StageFormatDiscovery  ExtractionStage = "format_discovery"
	StageFormatFiltering  ExtractionStage = "format_filtering"
	StageHLSManifestFetch ExtractionStage = "hls_manifest_fetch"
)

// StageDiagnostic records sanitized timing, status, and outcome of an individual extraction stage.
type StageDiagnostic struct {
	Name           ExtractionStage `json:"name"`
	ExecutionOrder int             `json:"execution_order"`
	DurationMs     int64           `json:"duration_ms"`
	HTTPStatus     int             `json:"http_status,omitempty"`
	Success        bool            `json:"success"`
	ErrorMessage   string          `json:"error_message,omitempty"`
}

// SessionInputDiagnostics records the presence (never values) of session identifiers and provider configs.
type SessionInputDiagnostics struct {
	VisitorDataPresent        bool   `json:"visitor_data_present"`
	POTokenProviderConfigured bool   `json:"po_token_provider_configured"`
	POTokenContext            string `json:"po_token_context,omitempty"`
	POTokenAcquired           bool   `json:"po_token_acquired"`
	POTokenExpiresInSeconds   *int64 `json:"po_token_expires_in_seconds,omitempty"`
	ChallengeSolverConfigured bool   `json:"challenge_solver_configured"`
}

// FormatInventoryDiagnostics records format counts and streaming endpoint capabilities.
type FormatInventoryDiagnostics struct {
	TotalDiscovered         int  `json:"total_discovered"`
	AudioOnlyCount          int  `json:"audio_only_count"`
	VideoOnlyCount          int  `json:"video_only_count"`
	ProgressiveCount        int  `json:"progressive_count"`
	HLSManifestPresent      bool `json:"hls_manifest_present"`
	DASHManifestPresent     bool `json:"dash_manifest_present"`
	SABREndpointPresent     bool `json:"sabr_endpoint_present"`
	SABRRejected            bool `json:"sabr_rejected"`
	SignatureChallengeCount int  `json:"signature_challenge_count"`
	NChallengeCount         int  `json:"n_challenge_count"`
	DirectDownloadableCount int  `json:"direct_downloadable_count"`
}

// ResolutionComparisonDiagnostics compares available resolutions with the requested selection.
type ResolutionComparisonDiagnostics struct {
	HighestAvailableHeight int      `json:"highest_available_height"`
	SelectedHeight         int      `json:"selected_height,omitempty"`
	SelectedFormatIDs      []string `json:"selected_format_ids,omitempty"`
}

// ExtractionDiagnostics is the comprehensive, sanitized diagnostic report for an extraction attempt.
// It contains STRICTLY ALLOWLISTED fields and ZERO secrets (no tokens, visitor IDs, cookies, auth headers,
// signed URLs, or raw API bodies).
type ExtractionDiagnostics struct {
	Version              string                          `json:"version"`
	OS                   string                          `json:"os"`
	Arch                 string                          `json:"arch"`
	Client               string                          `json:"client"`
	VideoID              string                          `json:"video_id"`
	TotalDurationMs      int64                           `json:"total_duration_ms"`
	Stages               []StageDiagnostic               `json:"stages"`
	SessionInputs        SessionInputDiagnostics         `json:"session_inputs"`
	FormatInventory      FormatInventoryDiagnostics      `json:"format_inventory"`
	ResolutionComparison ResolutionComparisonDiagnostics `json:"resolution_comparison"`
	PlaybackStatus       string                          `json:"playback_status,omitempty"`
	PlaybackReason       string                          `json:"playback_reason,omitempty"`
	FinalStatus          string                          `json:"final_status"` // "success" or "failed"
	FailureStage         string                          `json:"failure_stage,omitempty"`
	FailureCode          string                          `json:"failure_code,omitempty"`
	FailureReason        string                          `json:"failure_reason,omitempty"`
}

// FormatTextReport formats the extraction diagnostics into a human-readable, sanitized text report.
func (d *ExtractionDiagnostics) FormatTextReport() string {
	if d == nil {
		return "<nil diagnostics>"
	}
	var b strings.Builder
	b.WriteString("=== YouTube Extraction Diagnostics ===\n")
	fmt.Fprintf(&b, "Environment:      %s/%s (goyt %s)\n", d.OS, d.Arch, d.Version)
	fmt.Fprintf(&b, "Client:           %s", d.Client)
	if d.VideoID != "" {
		fmt.Fprintf(&b, " (Video ID: %s)", d.VideoID)
	}
	b.WriteString("\n")
	if d.PlaybackStatus != "" {
		if d.PlaybackReason != "" {
			fmt.Fprintf(&b, "Playback Status:  %s (%s)\n", d.PlaybackStatus, d.PlaybackReason)
		} else {
			fmt.Fprintf(&b, "Playback Status:  %s\n", d.PlaybackStatus)
		}
	}
	if d.FinalStatus == "success" {
		fmt.Fprintf(&b, "Final Outcome:    SUCCESS (total elapsed: %dms)\n", d.TotalDurationMs)
	} else {
		stageStr := d.FailureStage
		if stageStr == "" {
			stageStr = "unknown"
		}
		if d.FailureCode != "" {
			fmt.Fprintf(&b, "Final Outcome:    FAILED at stage '%s' (code: %s)\n", stageStr, d.FailureCode)
		} else {
			fmt.Fprintf(&b, "Final Outcome:    FAILED at stage '%s'\n", stageStr)
		}
	}

	if len(d.Stages) > 0 {
		b.WriteString("\nExtraction Stages:\n")
		tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "  #\tSTAGE\tSTATUS\tHTTP\tDURATION\tDETAILS")
		for _, s := range d.Stages {
			statusStr := "OK"
			if !s.Success {
				if s.HTTPStatus >= 200 && s.HTTPStatus < 400 {
					statusStr = "REJECTED"
				} else {
					statusStr = "FAILED"
				}
			}
			httpStr := "-"
			if s.HTTPStatus > 0 {
				httpStr = fmt.Sprintf("%d", s.HTTPStatus)
			}
			details := "-"
			if s.ErrorMessage != "" {
				details = s.ErrorMessage
			}
			fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%dms\t%s\n", s.ExecutionOrder, s.Name, statusStr, httpStr, s.DurationMs, details)
		}
		_ = tw.Flush()
	}

	b.WriteString("\nSession Inputs:\n")
	fmt.Fprintf(&b, "  Visitor data present:         %t\n", d.SessionInputs.VisitorDataPresent)
	fmt.Fprintf(&b, "  PO-token provider configured: %t\n", d.SessionInputs.POTokenProviderConfigured)
	if d.SessionInputs.POTokenProviderConfigured {
		if d.SessionInputs.POTokenContext != "" {
			fmt.Fprintf(&b, "  PO-token context:             %s\n", d.SessionInputs.POTokenContext)
		}
		fmt.Fprintf(&b, "  PO-token acquired:            %t\n", d.SessionInputs.POTokenAcquired)
		if d.SessionInputs.POTokenExpiresInSeconds != nil {
			fmt.Fprintf(&b, "  PO-token expires in:          %ds\n", *d.SessionInputs.POTokenExpiresInSeconds)
		}
	}
	fmt.Fprintf(&b, "  Challenge solver configured:  %t\n", d.SessionInputs.ChallengeSolverConfigured)

	if d.FormatInventory.TotalDiscovered > 0 {
		b.WriteString("\nFormat Inventory:\n")
		fmt.Fprintf(&b, "  Total stream formats: %d (audio: %d, video: %d, progressive: %d)\n",
			d.FormatInventory.TotalDiscovered,
			d.FormatInventory.AudioOnlyCount,
			d.FormatInventory.VideoOnlyCount,
			d.FormatInventory.ProgressiveCount,
		)
		fmt.Fprintf(&b, "  Streaming endpoints:  HLS=%t, DASH=%t, SABR=%t\n",
			d.FormatInventory.HLSManifestPresent,
			d.FormatInventory.DASHManifestPresent,
			d.FormatInventory.SABREndpointPresent,
		)
		if d.FormatInventory.SignatureChallengeCount > 0 || d.FormatInventory.NChallengeCount > 0 {
			fmt.Fprintf(&b, "  Challenged formats:   signature=%d, n_parameter=%d\n",
				d.FormatInventory.SignatureChallengeCount,
				d.FormatInventory.NChallengeCount,
			)
		}
		fmt.Fprintf(&b, "  Direct downloadable:  %d formats\n", d.FormatInventory.DirectDownloadableCount)
	}

	if d.ResolutionComparison.HighestAvailableHeight > 0 {
		b.WriteString("\nResolution Comparison:\n")
		fmt.Fprintf(&b, "  Highest available:    %dp\n", d.ResolutionComparison.HighestAvailableHeight)
		if d.ResolutionComparison.SelectedHeight > 0 {
			if len(d.ResolutionComparison.SelectedFormatIDs) > 0 {
				fmt.Fprintf(&b, "  Selected:             %dp (formats: %s)\n",
					d.ResolutionComparison.SelectedHeight,
					strings.Join(d.ResolutionComparison.SelectedFormatIDs, ", "),
				)
			} else {
				fmt.Fprintf(&b, "  Selected:             %dp\n", d.ResolutionComparison.SelectedHeight)
			}
		}
	}

	if d.FinalStatus != "success" && (d.FailureStage != "" || d.FailureCode != "") {
		b.WriteString("\nFailure Summary:\n")
		if d.FailureStage != "" {
			fmt.Fprintf(&b, "  Stage:  %s\n", d.FailureStage)
		}
		if d.FailureCode != "" {
			fmt.Fprintf(&b, "  Code:   %s\n", d.FailureCode)
		}
		if d.FailureReason != "" {
			fmt.Fprintf(&b, "  Reason: %s\n", d.FailureReason)
		}
	}

	b.WriteString("======================================\n")
	return b.String()
}

type diagBuilder struct {
	start       time.Time
	diagnostics ExtractionDiagnostics
	order       int
}

func newDiagBuilder(clientName, videoID string) *diagBuilder {
	return &diagBuilder{
		start: time.Now(),
		diagnostics: ExtractionDiagnostics{
			Version: goyt.Version,
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
			Client:  clientName,
			VideoID: videoID,
			Stages:  make([]StageDiagnostic, 0, 8),
		},
	}
}

func (b *diagBuilder) recordStage(name ExtractionStage, duration time.Duration, httpStatus int, success bool, err error) {
	b.order++
	errMsg := ""
	if err != nil {
		var extErr *ExtractionError
		if errors.As(err, &extErr) {
			if extErr.Code != "" {
				errMsg = sanitizeErrorMessage(fmt.Sprintf("%s: %s", extErr.Code, extErr.Message))
			} else {
				errMsg = sanitizeErrorMessage(extErr.Message)
			}
		} else {
			classified := classifyExtractionFailure(b.diagnostics.Client, err)
			errMsg = sanitizeErrorMessage(classified.Message)
		}
	}
	b.diagnostics.Stages = append(b.diagnostics.Stages, StageDiagnostic{
		Name:           name,
		ExecutionOrder: b.order,
		DurationMs:     duration.Milliseconds(),
		HTTPStatus:     httpStatus,
		Success:        success,
		ErrorMessage:   errMsg,
	})
}

func (b *diagBuilder) populateInventory(player *playerResponse, rawFormats []playerFormat, downloadableCount int) {
	if player == nil {
		return
	}
	inv := FormatInventoryDiagnostics{
		TotalDiscovered:         len(rawFormats),
		HLSManifestPresent:      player.StreamingData.HLSManifestURL != "",
		DASHManifestPresent:     player.StreamingData.DASHManifestURL != "",
		SABREndpointPresent:     player.StreamingData.ServerABRStreamingURL != "",
		SABRRejected:            player.StreamingData.ServerABRStreamingURL != "",
		DirectDownloadableCount: downloadableCount,
	}

	maxHeight := 0
	for _, f := range rawFormats {
		isVideo := strings.HasPrefix(f.MIMEType, "video/")
		isAudio := strings.HasPrefix(f.MIMEType, "audio/")
		isProgressive := false
		for _, pf := range player.StreamingData.Formats {
			if pf.Itag == f.Itag {
				isProgressive = true
				break
			}
		}

		if isProgressive {
			inv.ProgressiveCount++
		} else if isVideo {
			inv.VideoOnlyCount++
		} else if isAudio {
			inv.AudioOnlyCount++
		}

		cipher := f.SignatureCipher
		if cipher == "" {
			cipher = f.Cipher
		}
		if cipher != "" {
			inv.SignatureChallengeCount++
		}
		if f.URL != "" {
			if u, err := url.Parse(f.URL); err == nil && u.Query().Get("n") != "" {
				inv.NChallengeCount++
			}
		}
		if f.Height > maxHeight {
			maxHeight = f.Height
		}
	}

	b.diagnostics.FormatInventory = inv
	b.diagnostics.ResolutionComparison.HighestAvailableHeight = maxHeight
}

func (b *diagBuilder) finishSuccess(player *playerResponse, rawFormats []playerFormat, downloadableCount int) *ExtractionDiagnostics {
	b.diagnostics.TotalDurationMs = time.Since(b.start).Milliseconds()
	b.diagnostics.FinalStatus = "success"
	if player != nil {
		b.diagnostics.PlaybackStatus = player.PlayabilityStatus.Status
		b.diagnostics.PlaybackReason = sanitizeErrorMessage(player.PlayabilityStatus.Reason)
		b.populateInventory(player, rawFormats, downloadableCount)
	}
	res := b.diagnostics
	return &res
}

func (b *diagBuilder) finishFailure(stage ExtractionStage, err error, extErr *ExtractionError) *ExtractionDiagnostics {
	b.diagnostics.TotalDurationMs = time.Since(b.start).Milliseconds()
	b.diagnostics.FinalStatus = "failed"
	b.diagnostics.FailureStage = string(stage)
	if extErr != nil {
		b.diagnostics.FailureCode = extErr.Code
		b.diagnostics.FailureReason = sanitizeErrorMessage(extErr.Message)
		if extErr.PlaybackStatus != "" {
			b.diagnostics.PlaybackStatus = extErr.PlaybackStatus
		}
		if extErr.PlaybackReason != "" {
			b.diagnostics.PlaybackReason = sanitizeErrorMessage(extErr.PlaybackReason)
		}
	} else if err != nil {
		classified := classifyExtractionFailure(b.diagnostics.Client, err)
		b.diagnostics.FailureCode = classified.Code
		b.diagnostics.FailureReason = sanitizeErrorMessage(classified.Message)
	}
	res := b.diagnostics
	return &res
}
