package jssolver

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Supported runtime identifiers.
const (
	RuntimeAuto = "auto"
	RuntimeNode = "node"
	RuntimeDeno = "deno"
	RuntimeBun  = "bun"
	RuntimeQJS  = "qjs"
)

var autoCandidates = []string{RuntimeNode, RuntimeDeno, RuntimeBun, RuntimeQJS}

// ErrNoRuntimeFound is returned when no supported JavaScript runtime is discovered.
var ErrNoRuntimeFound = errors.New("jssolver: no supported JavaScript runtime found; install node, deno, bun, or qjs, or specify a runtime via -js-runtime")

// ResolveRuntime inspects PATH or an explicit file path to find and validate a JS runtime.
// If an explicit file path is provided, the runtime kind is determined by inspecting the executable's
// basename for "deno", "bun", "qjs", or "quickjs", and defaults to "node" (standard Node.js CLI interface).
func ResolveRuntime(nameOrPath string) (string, string, error) {
	nameOrPath = strings.TrimSpace(nameOrPath)
	if nameOrPath == "" || nameOrPath == RuntimeAuto {
		for _, cand := range autoCandidates {
			if path, err := exec.LookPath(cand); err == nil {
				abs, err := filepath.Abs(path)
				if err != nil {
					abs = path
				}
				return abs, cand, nil
			}
		}
		return "", "", ErrNoRuntimeFound
	}

	// Check if nameOrPath is a standard name
	lower := strings.ToLower(nameOrPath)
	switch lower {
	case RuntimeNode, RuntimeDeno, RuntimeBun, RuntimeQJS:
		path, err := exec.LookPath(lower)
		if err != nil {
			return "", "", fmt.Errorf("jssolver: runtime %q not found in PATH: %w", lower, err)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		return abs, lower, nil
	}

	// Custom executable path
	info, err := os.Stat(nameOrPath)
	if err != nil {
		return "", "", fmt.Errorf("jssolver: runtime executable not found at %q: %w", nameOrPath, err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("jssolver: runtime path %q is a directory, expected executable file", nameOrPath)
	}

	abs, err := filepath.Abs(nameOrPath)
	if err != nil {
		abs = nameOrPath
	}

	base := strings.ToLower(filepath.Base(nameOrPath))
	kind := RuntimeNode
	switch {
	case strings.Contains(base, "deno"):
		kind = RuntimeDeno
	case strings.Contains(base, "bun"):
		kind = RuntimeBun
	case strings.Contains(base, "qjs") || strings.Contains(base, "quickjs"):
		kind = RuntimeQJS
	}

	return abs, kind, nil
}

// BuildRuntimeArgs constructs the execution arguments for the given runtime kind and script path.
//
// Isolation Note:
//   - Deno applies fine-grained permission flags: --no-prompt disables interactive permission prompts,
//     and --allow-read restricts filesystem access solely to the temporary solver bundle script.
//     Deno does not grant network (--allow-net), file write (--allow-write), environment (--allow-env),
//     or child process execution (--allow-run) access.
//   - Node, Bun, and QuickJS execute as standard unconfined sub-processes with bounded stdio; they do not
//     provide fine-grained kernel sandboxing without external OS containerization (e.g. cgroups/jails).
func BuildRuntimeArgs(runtimeKind, scriptPath string) []string {
	switch runtimeKind {
	case RuntimeDeno:
		// Deno security flags: only allow read access to the script file itself (accounting for symlinked temp dirs).
		// --no-prompt ensures immediate permission denial rather than interactive prompts.
		readPaths := scriptPath
		if realPath, err := filepath.EvalSymlinks(scriptPath); err == nil && realPath != scriptPath {
			readPaths = scriptPath + "," + realPath
		}
		return []string{"run", "--no-prompt", "--allow-read=" + readPaths, scriptPath}
	case RuntimeBun:
		return []string{"run", scriptPath}
	case RuntimeNode, RuntimeQJS:
		fallthrough
	default:
		return []string{scriptPath}
	}
}
