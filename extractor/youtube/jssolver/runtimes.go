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

var autoCandidates = []string{RuntimeDeno, RuntimeNode, RuntimeBun, RuntimeQJS}

// ErrNoRuntimeFound is returned when no supported JavaScript runtime is discovered.
var ErrNoRuntimeFound = errors.New("jssolver: no supported JavaScript runtime found; install node, deno, bun, or qjs, or specify a runtime via -js-runtime")

// ResolveRuntime inspects PATH or an explicit file path to find and validate a JS runtime.
//
// Explicit paths can specify the runtime type using a prefix (e.g. "deno:/usr/local/bin/my-deno",
// "node:/opt/node/bin/node", "bun:/custom/bun", "qjs:/usr/bin/qjs"), or by having a recognizable
// executable basename containing "deno", "bun", "qjs"/"quickjs", or "node"/"nodejs".
// Custom paths with unrecognized runtime types are rejected with an explicit error.
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

	// Check for explicit type prefix: "type:path" (e.g. "deno:/custom/path/bin")
	if idx := strings.Index(nameOrPath, ":"); idx > 0 && idx < len(nameOrPath)-1 {
		prefix := strings.ToLower(nameOrPath[:idx])
		rawPath := nameOrPath[idx+1:]
		var explicitKind string
		switch prefix {
		case RuntimeNode, "nodejs":
			explicitKind = RuntimeNode
		case RuntimeDeno:
			explicitKind = RuntimeDeno
		case RuntimeBun:
			explicitKind = RuntimeBun
		case RuntimeQJS, "quickjs":
			explicitKind = RuntimeQJS
		}
		if explicitKind != "" {
			info, err := os.Stat(rawPath)
			if err != nil {
				return "", "", fmt.Errorf("jssolver: runtime executable not found at %q: %w", rawPath, err)
			}
			if info.IsDir() {
				return "", "", fmt.Errorf("jssolver: runtime path %q is a directory, expected executable file", rawPath)
			}
			abs, err := filepath.Abs(rawPath)
			if err != nil {
				abs = rawPath
			}
			return abs, explicitKind, nil
		}
	}

	// Check if nameOrPath is a standard runtime name in PATH
	lower := strings.ToLower(nameOrPath)
	switch lower {
	case RuntimeNode, "nodejs":
		path, err := exec.LookPath("node")
		if err != nil {
			path, err = exec.LookPath("nodejs")
		}
		if err != nil {
			return "", "", fmt.Errorf("jssolver: runtime %q not found in PATH: %w", lower, err)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		return abs, RuntimeNode, nil
	case RuntimeDeno, RuntimeBun:
		path, err := exec.LookPath(lower)
		if err != nil {
			return "", "", fmt.Errorf("jssolver: runtime %q not found in PATH: %w", lower, err)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		return abs, lower, nil
	case RuntimeQJS, "quickjs":
		path, err := exec.LookPath("qjs")
		if err != nil {
			path, err = exec.LookPath("quickjs")
		}
		if err != nil {
			return "", "", fmt.Errorf("jssolver: runtime %q not found in PATH: %w", lower, err)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		return abs, RuntimeQJS, nil
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
	switch {
	case strings.Contains(base, "deno"):
		return abs, RuntimeDeno, nil
	case strings.Contains(base, "bun"):
		return abs, RuntimeBun, nil
	case strings.Contains(base, "qjs") || strings.Contains(base, "quickjs"):
		return abs, RuntimeQJS, nil
	case strings.Contains(base, "node"):
		return abs, RuntimeNode, nil
	default:
		return "", "", fmt.Errorf("jssolver: cannot determine JavaScript runtime type for %q; executable basename must contain node, deno, bun, or qjs, or specify runtime type with prefix (e.g. deno:%s)", nameOrPath, nameOrPath)
	}
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
