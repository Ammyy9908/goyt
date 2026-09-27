package youtube

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/ammyy9908/goyt"
)

// Safety limits for solver operations.
const (
	// MaxChallengeBatchSize limits the total number of challenges passed in a single batch.
	MaxChallengeBatchSize = 100

	// MaxTransformedValueLength limits the acceptable length of deciphered signatures and transformed n parameters.
	MaxTransformedValueLength = 2048
)

// ChallengeType identifies the kind of transformation required.
type ChallengeType string

const (
	ChallengeSignature ChallengeType = "signature"
	ChallengeNParam    ChallengeType = "n_parameter"
)

// SignatureChallenge describes a request to decipher a video stream signature.
type SignatureChallenge struct {
	ID           string // Unique challenge ID within the batch (e.g. "sig-0")
	CipherString string // Opaque input signature challenge (s)
	TargetParam  string // Target URL query parameter name (e.g. sp or "sig")
}

// NChallenge describes a request to transform a video stream n query parameter.
type NChallenge struct {
	ID       string // Unique challenge ID within the batch (e.g. "n-0")
	RawValue string // Opaque input n parameter value
}

// ChallengeBatch groups signature and n-parameter challenge requests for one extraction.
type ChallengeBatch struct {
	Signatures []SignatureChallenge
	NParams    []NChallenge
}

// IsEmpty returns true if there are no challenge requests in the batch.
func (b ChallengeBatch) IsEmpty() bool {
	return len(b.Signatures) == 0 && len(b.NParams) == 0
}

// ResolutionSource describes the origin of a challenge resolution.
type ResolutionSource string

const (
	ResolutionNotRequired ResolutionSource = "not_required"
	ResolutionRuntime     ResolutionSource = "runtime"
	ResolutionCache       ResolutionSource = "cache"
	ResolutionUnknown     ResolutionSource = "unknown"
	ResolutionFailed      ResolutionSource = "failed"
)

// ChallengeStatus describes the detection and resolution state of a challenge on a format.
type ChallengeStatus struct {
	Detected bool
	Resolved bool
	Source   ResolutionSource
}

func (s ChallengeStatus) String() string {
	if !s.Detected {
		return "not_required"
	}
	if s.Resolved {
		switch s.Source {
		case ResolutionRuntime:
			return "resolved(runtime)"
		case ResolutionCache:
			return "resolved(cache)"
		case ResolutionUnknown:
			return "resolved(unknown)"
		default:
			return "resolved(unknown)"
		}
	}
	return "unresolved"
}

// FormatDiagnosticKey returns a unique key for a format, distinguishing different audio tracks with the same itag.
func FormatDiagnosticKey(formatID, audioTrackID string) string {
	if audioTrackID == "" {
		return formatID
	}
	return formatID + ":" + audioTrackID
}

// FormatDiagnostics records the challenge resolution history of an extracted format.
type FormatDiagnostics struct {
	FormatID     string
	AudioTrackID string
	Signature    ChallengeStatus
	NParam       ChallengeStatus
}

func (d FormatDiagnostics) Key() string {
	return FormatDiagnosticKey(d.FormatID, d.AudioTrackID)
}

func (d FormatDiagnostics) String() string {
	if d.AudioTrackID != "" {
		return fmt.Sprintf("Format %s (%s): signature=%s, n=%s", d.FormatID, d.AudioTrackID, d.Signature.String(), d.NParam.String())
	}
	return fmt.Sprintf("Format %s: signature=%s, n=%s", d.FormatID, d.Signature.String(), d.NParam.String())
}

// DownloadableResult contains the extracted downloadable media and its result-scoped challenge diagnostics.
type DownloadableResult struct {
	Media       *goyt.Media
	Diagnostics map[string]FormatDiagnostics
}

// FormatDiagnostics returns the challenge resolution diagnostics for a format from this extraction result.
// If audioTrackID is specified, it prefers the track-specific diagnostic record.
func (r *DownloadableResult) FormatDiagnostics(formatID string, audioTrackID ...string) (FormatDiagnostics, bool) {
	if r == nil || r.Diagnostics == nil {
		return FormatDiagnostics{
			FormatID:  formatID,
			Signature: ChallengeStatus{Source: ResolutionNotRequired},
			NParam:    ChallengeStatus{Source: ResolutionNotRequired},
		}, false
	}
	trackID := ""
	if len(audioTrackID) > 0 {
		trackID = audioTrackID[0]
	}
	if trackID != "" {
		key := FormatDiagnosticKey(formatID, trackID)
		if d, ok := r.Diagnostics[key]; ok {
			return d, true
		}
	}
	if d, ok := r.Diagnostics[formatID]; ok {
		return d, true
	}
	// Fallback: search for any diagnostic with matching FormatID
	for _, d := range r.Diagnostics {
		if d.FormatID == formatID {
			return d, true
		}
	}
	return FormatDiagnostics{
		FormatID:     formatID,
		AudioTrackID: trackID,
		Signature:    ChallengeStatus{Source: ResolutionNotRequired},
		NParam:       ChallengeStatus{Source: ResolutionNotRequired},
	}, false
}

// AllFormatDiagnostics returns a copy of all format challenge diagnostics from this extraction result.
func (r *DownloadableResult) AllFormatDiagnostics() map[string]FormatDiagnostics {
	if r == nil || r.Diagnostics == nil {
		return nil
	}
	res := make(map[string]FormatDiagnostics, len(r.Diagnostics))
	for k, v := range r.Diagnostics {
		res[k] = v
	}
	return res
}

// SignatureResult contains the deciphered signature output for a specific SignatureChallenge.
type SignatureResult struct {
	ID         string
	Deciphered string
	Source     ResolutionSource
	Error      error
}

// NResult contains the transformed n-parameter output for a specific NChallenge.
type NResult struct {
	ID          string
	Transformed string
	Source      ResolutionSource
	Error       error
}

// ChallengeBatchResult contains the collected outputs from a ChallengeSolver.
type ChallengeBatchResult struct {
	Signatures map[string]SignatureResult
	NParams    map[string]NResult
}

// NewChallengeBatchResult initializes an empty ChallengeBatchResult.
func NewChallengeBatchResult() ChallengeBatchResult {
	return ChallengeBatchResult{
		Signatures: make(map[string]SignatureResult),
		NParams:    make(map[string]NResult),
	}
}

// PlayerScript represents the identified and downloaded player JavaScript code.
type PlayerScript struct {
	URL           string // Validated canonical URL of the player script
	VersionID     string // Distinct player script version or identifier from URL
	ContentSHA256 string // Hex SHA-256 digest of the script content source
	Source        []byte // Script source code (bounded size)
}

// NewPlayerScript creates a PlayerScript with a calculated SHA-256 digest.
func NewPlayerScript(urlStr string, versionID string, source []byte) PlayerScript {
	h := sha256.Sum256(source)
	digest := hex.EncodeToString(h[:])
	return PlayerScript{
		URL:           urlStr,
		VersionID:     versionID,
		ContentSHA256: digest,
		Source:        source,
	}
}

// Identity returns a canonical composite identity string preventing cross-script conflation.
func (s PlayerScript) Identity() string {
	return fmt.Sprintf("%s#sha256=%s", s.URL, s.ContentSHA256)
}

// ChallengeSolver defines the replaceable interface for solving YouTube JS challenges.
// Implementations MUST be safe for concurrent use by multiple goroutines across concurrent extractions.
type ChallengeSolver interface {
	// SolveChallenges executes signature deciphering and/or n transformations using the player script.
	// Solvers must respect context cancellation, return structured results mapped by challenge ID,
	// and be safe for concurrent execution.
	SolveChallenges(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error)
}

// ErrNoSolverConfigured is returned when challenge solving is needed but no solver is configured.
var ErrNoSolverConfigured = errors.New("youtube: no challenge solver configured")

// ErrInvalidSolverResult is returned when a solver result is mismatched, missing, or malformed.
var ErrInvalidSolverResult = errors.New("youtube: invalid solver result association")
