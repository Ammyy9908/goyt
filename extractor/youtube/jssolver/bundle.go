package jssolver

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// SolverBundleVersion identifies the immutable AST solver bundle version for cache isolation.
const SolverBundleVersion = "v0.8.0-ast1"

//go:embed bundle.js
var solverBundleJS string

var (
	bundleMu       sync.Mutex
	bundleFilePath string
	bundleFileErr  error
)

// EnsureBundleScript writes the embedded solver JavaScript bundle to a secure temporary file
// with restricted permissions (0600) and returns its absolute path.
// The file path is cached in memory across subsequent calls.
func EnsureBundleScript() (string, error) {
	bundleMu.Lock()
	defer bundleMu.Unlock()

	if bundleFilePath != "" {
		if _, err := os.Stat(bundleFilePath); err == nil {
			return bundleFilePath, nil
		}
		// If the file was removed externally, recreate it.
		bundleFilePath = ""
		bundleFileErr = nil
	}

	tmpDir := os.TempDir()
	// Use .cjs extension for universal CommonJS compatibility across Node, Deno, Bun, and QuickJS.
	f, err := os.CreateTemp(tmpDir, "goyt-solver-*.cjs")
	if err != nil {
		bundleFileErr = fmt.Errorf("jssolver: create temp bundle script: %w", err)
		return "", bundleFileErr
	}
	defer f.Close()

	if err := f.Chmod(0600); err != nil {
		_ = os.Remove(f.Name())
		bundleFileErr = fmt.Errorf("jssolver: chmod temp bundle script: %w", err)
		return "", bundleFileErr
	}

	if _, err := f.WriteString(solverBundleJS); err != nil {
		_ = os.Remove(f.Name())
		bundleFileErr = fmt.Errorf("jssolver: write temp bundle script: %w", err)
		return "", bundleFileErr
	}

	absPath, err := filepath.Abs(f.Name())
	if err != nil {
		bundleFilePath = f.Name()
	} else {
		bundleFilePath = absPath
	}

	return bundleFilePath, nil
}

// CleanupBundleScript removes the temporary bundle script file if it was created.
func CleanupBundleScript() error {
	bundleMu.Lock()
	defer bundleMu.Unlock()

	if bundleFilePath != "" {
		err := os.Remove(bundleFilePath)
		bundleFilePath = ""
		bundleFileErr = nil
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
