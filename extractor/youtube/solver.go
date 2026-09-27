package youtube

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

// SignatureResult contains the deciphered signature output for a specific SignatureChallenge.
type SignatureResult struct {
	ID         string
	Deciphered string
	Error      error
}

// NResult contains the transformed n-parameter output for a specific NChallenge.
type NResult struct {
	ID          string
	Transformed string
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
