package jssolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ammyy9908/goyt/extractor/youtube"
)

const (
	// MaxSolverOutputBytes limits stdout reading from the solver process to 10 MB.
	MaxSolverOutputBytes = 10 << 20

	// MaxSolverStderrBytes limits stderr capture for diagnostics to 64 KB.
	MaxSolverStderrBytes = 64 << 10

	// DefaultSolverTimeout is the maximum time allowed for a single challenge execution batch.
	DefaultSolverTimeout = 15 * time.Second
)

// Option configures the JavaScript ChallengeSolver.
type Option func(*Solver)

// WithRuntime configures the runtime name or executable path (e.g. "node", "deno", "bun", "qjs", "auto", or "/path/to/bin").
func WithRuntime(runtimeNameOrPath string) Option {
	return func(s *Solver) {
		s.runtimeConfig = runtimeNameOrPath
	}
}

// WithTimeout sets the per-invocation timeout for solver child processes.
func WithTimeout(d time.Duration) Option {
	return func(s *Solver) {
		if d > 0 {
			s.timeout = d
		}
	}
}

// WithCacheCapacity configures the in-memory LRU cache capacity.
func WithCacheCapacity(capacity int) Option {
	return func(s *Solver) {
		s.cache = NewBoundedChallengeCache(capacity)
	}
}

// WithScriptPath overrides the path to the solver bundle script (used for testing or custom bundles).
func WithScriptPath(path string) Option {
	return func(s *Solver) {
		s.scriptPath = path
	}
}

// Solver implements youtube.ChallengeSolver by delegating AST deobfuscation and execution
// to an external JavaScript runtime (Node, Deno, Bun, or QuickJS) via a bundled solver engine.
type Solver struct {
	runtimeConfig string
	runtimePath   string
	runtimeKind   string
	scriptPath    string
	cache         *BoundedChallengeCache
	timeout       time.Duration
}

var _ youtube.ChallengeSolver = (*Solver)(nil)

// New initializes an external JavaScript ChallengeSolver.
func New(opts ...Option) (*Solver, error) {
	s := &Solver{
		runtimeConfig: RuntimeAuto,
		cache:         NewBoundedChallengeCache(DefaultCacheCapacity),
		timeout:       DefaultSolverTimeout,
	}

	for _, opt := range opts {
		opt(s)
	}

	resolvedPath, runtimeKind, err := ResolveRuntime(s.runtimeConfig)
	if err != nil {
		return nil, err
	}
	s.runtimePath = resolvedPath
	s.runtimeKind = runtimeKind

	if s.scriptPath == "" {
		scriptPath, err := EnsureBundleScript()
		if err != nil {
			return nil, err
		}
		s.scriptPath = scriptPath
	}

	return s, nil
}

// RuntimePath returns the resolved path of the underlying JS runtime.
func (s *Solver) RuntimePath() string {
	return s.runtimePath
}

// RuntimeKind returns the category of runtime (e.g. "node", "deno", "bun", "qjs").
func (s *Solver) RuntimeKind() string {
	return s.runtimeKind
}

type solverRequest struct {
	ProtocolVersion int      `json:"protocol_version"`
	Player          string   `json:"player"`
	Signatures      []string `json:"signatures,omitempty"`
	NParams         []string `json:"n_params,omitempty"`
}

type solverResponse struct {
	ProtocolVersion int               `json:"protocol_version"`
	Signatures      map[string]string `json:"signatures"`
	NParams         map[string]string `json:"n_params"`
	Errors          map[string]string `json:"errors,omitempty"`
	Error           string            `json:"error,omitempty"`
}

// SolveChallenges executes signature deciphering and n-parameter transformation
// using the player script and the configured external JavaScript runtime.
func (s *Solver) SolveChallenges(
	ctx context.Context,
	script youtube.PlayerScript,
	batch youtube.ChallengeBatch,
) (youtube.ChallengeBatchResult, error) {
	result := youtube.NewChallengeBatchResult()
	if batch.IsEmpty() {
		return result, nil
	}

	if len(script.Source) == 0 {
		return result, errors.New("jssolver: empty player script")
	}

	// 1. Check in-memory LRU cache for already solved challenges using structured keys.
	sigToIDs := make(map[string][]string)
	var uncachedSigs []string

	for _, sig := range batch.Signatures {
		if sig.CipherString == "" {
			continue
		}
		cacheKey := formatCacheKey("sig", script.Identity(), sig.CipherString)
		if cached, ok := s.cache.Get(cacheKey); ok {
			result.Signatures[sig.ID] = youtube.SignatureResult{
				ID:         sig.ID,
				Deciphered: cached,
				Source:     youtube.ResolutionCache,
			}
		} else {
			if _, exists := sigToIDs[sig.CipherString]; !exists {
				uncachedSigs = append(uncachedSigs, sig.CipherString)
			}
			sigToIDs[sig.CipherString] = append(sigToIDs[sig.CipherString], sig.ID)
		}
	}

	nToIDs := make(map[string][]string)
	var uncachedNParams []string

	for _, n := range batch.NParams {
		if n.RawValue == "" {
			continue
		}
		cacheKey := formatCacheKey("n", script.Identity(), n.RawValue)
		if cached, ok := s.cache.Get(cacheKey); ok {
			result.NParams[n.ID] = youtube.NResult{
				ID:          n.ID,
				Transformed: cached,
				Source:      youtube.ResolutionCache,
			}
		} else {
			if _, exists := nToIDs[n.RawValue]; !exists {
				uncachedNParams = append(uncachedNParams, n.RawValue)
			}
			nToIDs[n.RawValue] = append(nToIDs[n.RawValue], n.ID)
		}
	}

	// If all challenges were resolved from cache, return immediately.
	if len(uncachedSigs) == 0 && len(uncachedNParams) == 0 {
		return result, nil
	}

	// 2. Prepare execution context and process invocation.
	execCtx := ctx
	var cancel context.CancelFunc
	if s.timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	reqPayload := solverRequest{
		ProtocolVersion: 1,
		Player:          string(script.Source),
		Signatures:      uncachedSigs,
		NParams:         uncachedNParams,
	}

	payloadBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return result, fmt.Errorf("jssolver: marshal request: %w", err)
	}

	args := BuildRuntimeArgs(s.runtimeKind, s.scriptPath)
	cmd := exec.CommandContext(execCtx, s.runtimePath, args...)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return result, fmt.Errorf("jssolver: stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return result, fmt.Errorf("jssolver: stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		return result, fmt.Errorf("jssolver: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return result, fmt.Errorf("jssolver: start runtime %s: %w", s.runtimeKind, err)
	}

	var (
		stdoutBuf        bytes.Buffer
		stderrBuf        bytes.Buffer
		stdoutOverflowed bool
		wg               sync.WaitGroup
	)

	// Stdout reader: read up to MaxSolverOutputBytes + 1 to detect and handle overflow explicitly.
	wg.Add(1)
	go func() {
		defer wg.Done()
		limitedReader := io.LimitReader(stdoutPipe, MaxSolverOutputBytes+1)
		buf := make([]byte, 32*1024)
		for {
			n, rErr := limitedReader.Read(buf)
			if n > 0 {
				if int64(stdoutBuf.Len()+n) > MaxSolverOutputBytes {
					stdoutOverflowed = true
					// Terminate runaway child process immediately
					if cmd.Process != nil {
						_ = cmd.Process.Kill()
					}
					// Drain remainder of pipe to avoid blocking child
					_, _ = io.Copy(io.Discard, stdoutPipe)
					return
				}
				stdoutBuf.Write(buf[:n])
			}
			if rErr != nil {
				break
			}
		}
		// Drain remainder if any
		_, _ = io.Copy(io.Discard, stdoutPipe)
	}()

	// Stderr reader: capture up to MaxSolverStderrBytes and drain remainder so child never blocks.
	wg.Add(1)
	go func() {
		defer wg.Done()
		limitedStderr := io.LimitReader(stderrPipe, MaxSolverStderrBytes)
		_, _ = io.Copy(&stderrBuf, limitedStderr)
		_, _ = io.Copy(io.Discard, stderrPipe)
	}()

	// Write request to stdin and close
	go func() {
		_, _ = stdinPipe.Write(payloadBytes)
		_ = stdinPipe.Close()
	}()

	wg.Wait()
	runErr := cmd.Wait()

	if stdoutOverflowed {
		return result, fmt.Errorf("jssolver: runtime output exceeded %d bytes limit", MaxSolverOutputBytes)
	}

	if runErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return result, context.Canceled
		}
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return result, context.DeadlineExceeded
		}
		stderrMsg := sanitizeStderr(stderrBuf.String())
		if stderrMsg != "" {
			return result, fmt.Errorf("jssolver: runtime %s execution failed: %s: %w", s.runtimeKind, stderrMsg, runErr)
		}
		return result, fmt.Errorf("jssolver: runtime %s execution failed: %w", s.runtimeKind, runErr)
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}

	// 3. Parse and validate solver output.
	var resp solverResponse
	if err := json.Unmarshal(stdoutBuf.Bytes(), &resp); err != nil {
		return result, fmt.Errorf("jssolver: parse runtime response: %w", err)
	}

	if resp.Error != "" {
		return result, fmt.Errorf("jssolver: challenge solving failed: %s", sanitizeStderr(resp.Error))
	}

	// 4. Map signature results and update cache.
	for _, rawSig := range uncachedSigs {
		deciphered, ok := resp.Signatures[rawSig]
		ids := sigToIDs[rawSig]

		if ok && deciphered != "" && len(deciphered) <= youtube.MaxTransformedValueLength {
			cacheKey := formatCacheKey("sig", script.Identity(), rawSig)
			s.cache.Set(cacheKey, deciphered)

			for _, id := range ids {
				result.Signatures[id] = youtube.SignatureResult{
					ID:         id,
					Deciphered: deciphered,
					Source:     youtube.ResolutionRuntime,
				}
			}
		} else {
			errMsg := resp.Errors["sig"]
			if errMsg == "" {
				errMsg = "signature challenge deciphering failed"
			}
			for _, id := range ids {
				result.Signatures[id] = youtube.SignatureResult{
					ID:     id,
					Source: youtube.ResolutionFailed,
					Error:  errors.New(errMsg),
				}
			}
		}
	}

	// 5. Map n-param results and update cache.
	for _, rawN := range uncachedNParams {
		transformed, ok := resp.NParams[rawN]
		ids := nToIDs[rawN]

		if ok && transformed != "" && len(transformed) <= youtube.MaxTransformedValueLength {
			cacheKey := formatCacheKey("n", script.Identity(), rawN)
			s.cache.Set(cacheKey, transformed)

			for _, id := range ids {
				result.NParams[id] = youtube.NResult{
					ID:          id,
					Transformed: transformed,
					Source:      youtube.ResolutionRuntime,
				}
			}
		} else {
			errMsg := resp.Errors["n"]
			if errMsg == "" {
				errMsg = "n-parameter transformation failed"
			}
			for _, id := range ids {
				result.NParams[id] = youtube.NResult{
					ID:     id,
					Source: youtube.ResolutionFailed,
					Error:  errors.New(errMsg),
				}
			}
		}
	}

	return result, nil
}

func formatCacheKey(kind, scriptIdentity, val string) string {
	return fmt.Sprintf("%s|%s|%s|%s", SolverBundleVersion, scriptIdentity, kind, val)
}

func sanitizeStderr(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 500 {
		raw = raw[:500] + "... (truncated)"
	}
	return raw
}
