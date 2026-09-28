package jssolver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ammyy9908/goyt/extractor/youtube"
)

func loadFixture(t *testing.T, filename string) []byte {
	t.Helper()
	path := filepath.Join("testdata", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read test fixture %s: %v", filename, err)
	}
	return data
}

func getTestSolver(t *testing.T) *Solver {
	t.Helper()
	solver, err := New(WithRuntime(RuntimeAuto))
	if err != nil {
		if os.Getenv("CI") != "" || os.Getenv("GOYT_REQUIRE_JS_RUNTIME") != "" {
			t.Fatalf("expected JS runtime in CI environment, but none found: %v", err)
		}
		t.Skipf("skipping test: no JS runtime available on host: %v", err)
	}
	return solver
}

func TestSolver_RealSignatureTransformation(t *testing.T) {
	solver := getTestSolver(t)

	playerGB := loadFixture(t, "player_7460dd14_en_GB.js")
	scriptGB := youtube.NewPlayerScript("https://www.youtube.com/s/player/7460dd14/base.js", "7460dd14_en_GB", playerGB)

	batch := youtube.ChallengeBatch{
		Signatures: []youtube.SignatureChallenge{
			{ID: "sig-1", CipherString: "ABCD1234EFGH5678", TargetParam: "sig"},
			{ID: "sig-2", CipherString: "TEST_SIG_2", TargetParam: "sig"},
		},
	}

	result, err := solver.SolveChallenges(context.Background(), scriptGB, batch)
	if err != nil {
		t.Fatalf("SolveChallenges error: %v", err)
	}

	// Verify known expected outputs obtained independently from JS execution
	wantSig1 := "65HGFE4"
	wantSig2 := "G"

	res1, ok1 := result.Signatures["sig-1"]
	if !ok1 || res1.Error != nil || res1.Deciphered != wantSig1 {
		t.Errorf("sig-1 got deciphered %q (err=%v), want %q", res1.Deciphered, res1.Error, wantSig1)
	}

	res2, ok2 := result.Signatures["sig-2"]
	if !ok2 || res2.Error != nil || res2.Deciphered != wantSig2 {
		t.Errorf("sig-2 got deciphered %q (err=%v), want %q", res2.Deciphered, res2.Error, wantSig2)
	}
}

func TestSolver_RealNTransformation(t *testing.T) {
	solver := getTestSolver(t)

	playerGB := loadFixture(t, "player_7460dd14_en_GB.js")
	scriptGB := youtube.NewPlayerScript("https://www.youtube.com/s/player/7460dd14/base.js", "7460dd14_en_GB", playerGB)

	batch := youtube.ChallengeBatch{
		NParams: []youtube.NChallenge{
			{ID: "n-1", RawValue: "M4F03qQkE9n8wA"},
			{ID: "n-2", RawValue: "SECOND_N_TOKEN"},
		},
	}

	result, err := solver.SolveChallenges(context.Background(), scriptGB, batch)
	if err != nil {
		t.Fatalf("SolveChallenges error: %v", err)
	}

	wantN1 := "7vBb38VB1P"
	wantN2 := "OxWvDJr8cp"

	res1, ok1 := result.NParams["n-1"]
	if !ok1 || res1.Error != nil || res1.Transformed != wantN1 {
		t.Errorf("n-1 got transformed %q (err=%v), want %q", res1.Transformed, res1.Error, wantN1)
	}

	res2, ok2 := result.NParams["n-2"]
	if !ok2 || res2.Error != nil || res2.Transformed != wantN2 {
		t.Errorf("n-2 got transformed %q (err=%v), want %q", res2.Transformed, res2.Error, wantN2)
	}
}

func TestSolver_CombinedChallengesAndMultiplePlayerIdentities(t *testing.T) {
	solver := getTestSolver(t)

	playerGB := loadFixture(t, "player_7460dd14_en_GB.js")
	playerUS := loadFixture(t, "player_7460dd14_en_US.js")

	scriptGB := youtube.NewPlayerScript("https://www.youtube.com/s/player/7460dd14/en_GB/base.js", "7460dd14_en_GB", playerGB)
	scriptUS := youtube.NewPlayerScript("https://www.youtube.com/s/player/7460dd14/en_US/base.js", "7460dd14_en_US", playerUS)

	if scriptGB.Identity() == scriptUS.Identity() {
		t.Fatalf("expected distinct player identities, got matching: %s", scriptGB.Identity())
	}

	combinedBatch := youtube.ChallengeBatch{
		Signatures: []youtube.SignatureChallenge{
			{ID: "sig-comb", CipherString: "ABCD1234EFGH5678", TargetParam: "sig"},
		},
		NParams: []youtube.NChallenge{
			{ID: "n-comb", RawValue: "M4F03qQkE9n8wA"},
		},
	}

	resGB, err := solver.SolveChallenges(context.Background(), scriptGB, combinedBatch)
	if err != nil {
		t.Fatalf("SolveChallenges GB failed: %v", err)
	}
	if resGB.Signatures["sig-comb"].Deciphered != "65HGFE4" {
		t.Errorf("GB decipher mismatch: got %q", resGB.Signatures["sig-comb"].Deciphered)
	}
	if resGB.NParams["n-comb"].Transformed != "7vBb38VB1P" {
		t.Errorf("GB n-param mismatch: got %q", resGB.NParams["n-comb"].Transformed)
	}

	resUS, err := solver.SolveChallenges(context.Background(), scriptUS, combinedBatch)
	if err != nil {
		t.Fatalf("SolveChallenges US failed: %v", err)
	}
	if resUS.Signatures["sig-comb"].Deciphered != "65HGFE4" {
		t.Errorf("US decipher mismatch: got %q", resUS.Signatures["sig-comb"].Deciphered)
	}
	if resUS.NParams["n-comb"].Transformed != "7vBb38VB1P" {
		t.Errorf("US n-param mismatch: got %q", resUS.NParams["n-comb"].Transformed)
	}
}

func TestSolver_LRUCache_AvoidsSubprocess(t *testing.T) {
	solver := getTestSolver(t)

	playerGB := loadFixture(t, "player_7460dd14_en_GB.js")
	scriptGB := youtube.NewPlayerScript("https://www.youtube.com/s/player/7460dd14/base.js", "7460dd14_en_GB", playerGB)

	batch := youtube.ChallengeBatch{
		Signatures: []youtube.SignatureChallenge{
			{ID: "sig-1", CipherString: "ABCD1234EFGH5678", TargetParam: "sig"},
		},
	}

	// 1. First execution (process spawned)
	res1, err := solver.SolveChallenges(context.Background(), scriptGB, batch)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if res1.Signatures["sig-1"].Deciphered != "65HGFE4" {
		t.Fatalf("first call wrong result: %q", res1.Signatures["sig-1"].Deciphered)
	}

	// 2. Second execution with canceled context should succeed instantly from cache
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	res2, err := solver.SolveChallenges(canceledCtx, scriptGB, batch)
	if err != nil {
		t.Fatalf("cached call should not fail on canceled context: %v", err)
	}
	if res2.Signatures["sig-1"].Deciphered != "65HGFE4" {
		t.Fatalf("cached call wrong result: %q", res2.Signatures["sig-1"].Deciphered)
	}
}

func TestSolver_CorruptedPlayerScript_FailsDescriptively(t *testing.T) {
	solver := getTestSolver(t)

	corrupted := []byte("function invalid_syntax {{{ NOT VALID JS }}}")
	script := youtube.NewPlayerScript("https://www.youtube.com/s/player/bad/base.js", "bad", corrupted)

	batch := youtube.ChallengeBatch{
		Signatures: []youtube.SignatureChallenge{
			{ID: "sig-1", CipherString: "TOKEN", TargetParam: "sig"},
		},
	}

	res, err := solver.SolveChallenges(context.Background(), script, batch)
	if err != nil {
		// Either error returned or per-item error mapped
		if !strings.Contains(err.Error(), "failed") && !strings.Contains(err.Error(), "jssolver") {
			t.Errorf("expected descriptive error message, got: %v", err)
		}
		return
	}

	sigRes, ok := res.Signatures["sig-1"]
	if !ok || sigRes.Error == nil {
		t.Fatal("expected error on corrupted player script, got success")
	}
}

func TestSolver_Cancellation_InterruptsProcess(t *testing.T) {
	solver := getTestSolver(t)

	playerGB := loadFixture(t, "player_7460dd14_en_GB.js")
	scriptGB := youtube.NewPlayerScript("https://www.youtube.com/s/player/7460dd14/base.js", "7460dd14_uncached", playerGB)

	batch := youtube.ChallengeBatch{
		Signatures: []youtube.SignatureChallenge{
			{ID: "sig-uncached", CipherString: fmt.Sprintf("TOKEN_%d", time.Now().UnixNano()), TargetParam: "sig"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-canceled

	_, err := solver.SolveChallenges(ctx, scriptGB, batch)
	if err == nil {
		t.Fatal("expected error on pre-canceled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestSolver_MissingRuntime_ActionableError(t *testing.T) {
	_, err := New(WithRuntime("nonexistent_binary_xyz_12345"))
	if err == nil {
		t.Fatal("expected error for missing runtime, got nil")
	}

	if !strings.Contains(err.Error(), "nonexistent_binary_xyz_12345") && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected actionable message mentioning runtime name, got: %v", err)
	}
}

func TestSolver_ConcurrentRaceExecution(t *testing.T) {
	solver := getTestSolver(t)

	playerGB := loadFixture(t, "player_7460dd14_en_GB.js")
	scriptGB := youtube.NewPlayerScript("https://www.youtube.com/s/player/7460dd14/base.js", "7460dd14_en_GB", playerGB)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			batch := youtube.ChallengeBatch{
				Signatures: []youtube.SignatureChallenge{
					{ID: fmt.Sprintf("sig-%d", idx), CipherString: "ABCD1234EFGH5678", TargetParam: "sig"},
				},
				NParams: []youtube.NChallenge{
					{ID: fmt.Sprintf("n-%d", idx), RawValue: "M4F03qQkE9n8wA"},
				},
			}
			res, err := solver.SolveChallenges(context.Background(), scriptGB, batch)
			if err != nil {
				t.Errorf("worker %d failed: %v", idx, err)
				return
			}
			if res.Signatures[fmt.Sprintf("sig-%d", idx)].Deciphered != "65HGFE4" {
				t.Errorf("worker %d wrong sig result", idx)
			}
			if res.NParams[fmt.Sprintf("n-%d", idx)].Transformed != "7vBb38VB1P" {
				t.Errorf("worker %d wrong n result", idx)
			}
		}(i)
	}
	wg.Wait()
}

func TestSolver_DenoSandboxFlags_ArgsValidation(t *testing.T) {
	args := BuildRuntimeArgs(RuntimeDeno, "/tmp/test-bundle.cjs")
	foundAllowRead := false
	foundNoPrompt := false
	for _, a := range args {
		if strings.HasPrefix(a, "--allow-read=") {
			foundAllowRead = true
		}
		if a == "--no-prompt" {
			foundNoPrompt = true
		}
		if strings.HasPrefix(a, "--allow-net") || strings.HasPrefix(a, "--allow-write") || strings.HasPrefix(a, "--allow-env") || strings.HasPrefix(a, "--allow-run") {
			t.Errorf("prohibited permission flag found in Deno arguments: %s", a)
		}
	}
	if !foundAllowRead {
		t.Error("expected --allow-read flag in Deno arguments")
	}
	if !foundNoPrompt {
		t.Error("expected --no-prompt flag in Deno arguments")
	}
}

func TestResolveRuntime_PrefixAndUnknownTypeRejection(t *testing.T) {
	// 1. Unrecognized executable without type prefix must be rejected
	tmpDir := t.TempDir()
	unknownExe := filepath.Join(tmpDir, "custom-unknown-engine")
	if err := os.WriteFile(unknownExe, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("failed to create dummy executable: %v", err)
	}

	_, _, err := ResolveRuntime(unknownExe)
	if err == nil {
		t.Fatalf("expected error resolving unknown runtime %q, got nil", unknownExe)
	}
	if !strings.Contains(err.Error(), "cannot determine JavaScript runtime type") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 2. Explicit type prefix allows custom executable names
	absPath, kind, err := ResolveRuntime("deno:" + unknownExe)
	if err != nil {
		t.Fatalf("expected successful resolution with deno: prefix, got: %v", err)
	}
	if kind != RuntimeDeno || absPath != unknownExe {
		t.Fatalf("expected (deno, %q), got (%s, %q)", unknownExe, kind, absPath)
	}

	absPathNode, kindNode, err := ResolveRuntime("node:" + unknownExe)
	if err != nil {
		t.Fatalf("expected successful resolution with node: prefix, got: %v", err)
	}
	if kindNode != RuntimeNode || absPathNode != unknownExe {
		t.Fatalf("expected (node, %q), got (%s, %q)", unknownExe, kindNode, absPathNode)
	}

	// 3. Basename recognition
	denoExe := filepath.Join(tmpDir, "my-deno-v2")
	if err := os.WriteFile(denoExe, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("failed to create dummy deno executable: %v", err)
	}
	_, kindDeno, err := ResolveRuntime(denoExe)
	if err != nil || kindDeno != RuntimeDeno {
		t.Fatalf("expected RuntimeDeno for %q, got (%s, %v)", denoExe, kindDeno, err)
	}
}

func TestSolver_DenoSandbox_BehavioralForbiddenAccess(t *testing.T) {
	denoPath, err := exec.LookPath("deno")
	if err != nil {
		if os.Getenv("GOYT_REQUIRE_DENO_RUNTIME") != "" {
			t.Fatalf("expected Deno in CI environment, but not found: %v", err)
		}
		t.Skip("skipping Deno behavioral test: deno not found on host")
	}

	// Create harmless local fixtures for testing read and write restrictions
	tmpDir := t.TempDir()
	forbiddenReadTarget := filepath.Join(tmpDir, "secret-data.txt")
	if err := os.WriteFile(forbiddenReadTarget, []byte("TOP_SECRET"), 0600); err != nil {
		t.Fatalf("failed to create read target: %v", err)
	}
	forbiddenWriteTarget := filepath.Join(tmpDir, "forbidden-output.txt")

	// Create a test script attempting all prohibited operations:
	// 1. Network access (fetch)
	// 2. Unauthorized file read
	// 3. Unauthorized file write
	// 4. Environment variable access
	// 5. Subprocess creation
	tmpScript, err := os.CreateTemp("", "deno-sandbox-behavior-*.js")
	if err != nil {
		t.Fatalf("failed to create temp test script: %v", err)
	}
	defer os.Remove(tmpScript.Name())

	scriptContent := fmt.Sprintf(`
		const results = {};

		// 1. Prohibited Network Access
		try {
			await fetch("http://127.0.0.1:1");
			results.network = "BREACH";
		} catch (e) {
			results.network = "BLOCKED";
		}

		// 2. Prohibited Unauthorized File Read
		try {
			Deno.readTextFileSync(%q);
			results.file_read = "BREACH";
		} catch (e) {
			results.file_read = "BLOCKED";
		}

		// 3. Prohibited Unauthorized File Write
		try {
			Deno.writeTextFileSync(%q, "MALICIOUS");
			results.file_write = "BREACH";
		} catch (e) {
			results.file_write = "BLOCKED";
		}

		// 4. Prohibited Environment Access
		try {
			const env = Deno.env.get("PATH");
			results.env_access = env ? "BREACH" : "BLOCKED";
		} catch (e) {
			results.env_access = "BLOCKED";
		}

		// 5. Prohibited Subprocess Execution
		try {
			if (typeof Deno.Command !== "undefined") {
				new Deno.Command("echo").outputSync();
				results.subprocess = "BREACH";
			} else if (typeof Deno.run !== "undefined") {
				Deno.run({ cmd: ["echo"] });
				results.subprocess = "BREACH";
			} else {
				results.subprocess = "BLOCKED";
			}
		} catch (e) {
			results.subprocess = "BLOCKED";
		}

		console.log(JSON.stringify(results));
	`, forbiddenReadTarget, forbiddenWriteTarget)

	if _, err := tmpScript.WriteString(scriptContent); err != nil {
		t.Fatalf("failed to write temp script: %v", err)
	}
	_ = tmpScript.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	args := BuildRuntimeArgs(RuntimeDeno, tmpScript.Name())
	cmd := exec.CommandContext(ctx, denoPath, args...)
	out, err := cmd.CombinedOutput()
	outStr := string(out)

	if ctx.Err() != nil {
		t.Fatalf("Deno sandbox test timed out (possible interactive prompt hang): %v", ctx.Err())
	}

	if strings.Contains(outStr, "BREACH") {
		t.Fatalf("Deno sandbox permission violation detected in output: %s", outStr)
	}

	if _, statErr := os.Stat(forbiddenWriteTarget); statErr == nil {
		t.Fatalf("Deno sandbox failed: prohibited write target %s was created", forbiddenWriteTarget)
	}

	t.Logf("Deno behavioral isolation successfully verified (denials enforced): %s", strings.TrimSpace(outStr))
}

func TestSolver_StdoutOverflow_TerminatesProcess(t *testing.T) {
	solver := getTestSolver(t)

	// Create a mock bundle script that attempts to output more than MaxSolverOutputBytes
	mockScript, err := os.CreateTemp("", "mock-overflow-*.cjs")
	if err != nil {
		t.Fatalf("failed to create mock script: %v", err)
	}
	defer os.Remove(mockScript.Name())

	// Write a loop generating 15 MB of output to stdout
	code := `
		const chunk = "X".repeat(1024 * 1024);
		for (let i = 0; i < 15; i++) {
			process.stdout.write(chunk);
		}
	`
	if _, err := mockScript.WriteString(code); err != nil {
		t.Fatalf("failed to write mock script: %v", err)
	}
	_ = mockScript.Close()

	overflowSolver, err := New(WithRuntime(solver.RuntimeKind()), WithScriptPath(mockScript.Name()))
	if err != nil {
		t.Fatalf("failed to create overflow test solver: %v", err)
	}

	script := youtube.NewPlayerScript("https://www.youtube.com/s/player/mock/base.js", "mock", []byte("mock source"))
	batch := youtube.ChallengeBatch{
		Signatures: []youtube.SignatureChallenge{
			{ID: "sig-over", CipherString: "TOKEN_OVERFLOW", TargetParam: "sig"},
		},
	}

	_, err = overflowSolver.SolveChallenges(context.Background(), script, batch)
	if err == nil {
		t.Fatal("expected error on oversized stdout output, got nil")
	}

	if !strings.Contains(err.Error(), "exceeded") && !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected output limit error message, got: %v", err)
	}
}

func TestSolver_LargeStderr_DoesNotBlockProcess(t *testing.T) {
	solver := getTestSolver(t)

	// Create a mock script that emits 200 KB to stderr, then outputs valid JSON to stdout
	mockScript, err := os.CreateTemp("", "mock-stderr-*.cjs")
	if err != nil {
		t.Fatalf("failed to create mock script: %v", err)
	}
	defer os.Remove(mockScript.Name())

	code := `
		// Write 200 KB to stderr
		for (let i = 0; i < 200; i++) {
			process.stderr.write("E".repeat(1024) + "\n");
		}
		// Write valid JSON to stdout
		const resp = {
			protocol_version: 1,
			signatures: { "SIG_IN": "SIG_OUT" },
			n_params: {}
		};
		process.stdout.write(JSON.stringify(resp));
	`
	if _, err := mockScript.WriteString(code); err != nil {
		t.Fatalf("failed to write mock script: %v", err)
	}
	_ = mockScript.Close()

	stderrSolver, err := New(WithRuntime(solver.RuntimeKind()), WithScriptPath(mockScript.Name()))
	if err != nil {
		t.Fatalf("failed to create stderr test solver: %v", err)
	}

	script := youtube.NewPlayerScript("https://www.youtube.com/s/player/mock/base.js", "mock", []byte("mock source"))
	batch := youtube.ChallengeBatch{
		Signatures: []youtube.SignatureChallenge{
			{ID: "sig-1", CipherString: "SIG_IN", TargetParam: "sig"},
		},
	}

	res, err := stderrSolver.SolveChallenges(context.Background(), script, batch)
	if err != nil {
		t.Fatalf("solver blocked or failed on large stderr: %v", err)
	}

	if res.Signatures["sig-1"].Deciphered != "SIG_OUT" {
		t.Fatalf("expected deciphered value 'SIG_OUT', got: %q", res.Signatures["sig-1"].Deciphered)
	}
}

func TestSolver_BundleLifecycle_CreationPermissionsAndCleanup(t *testing.T) {
	// Clean up any existing bundle file
	if err := CleanupBundleScript(); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	// 1. Ensure creation
	path1, err := EnsureBundleScript()
	if err != nil {
		t.Fatalf("EnsureBundleScript failed: %v", err)
	}
	if path1 == "" {
		t.Fatal("expected non-empty bundle path")
	}

	info, err := os.Stat(path1)
	if err != nil {
		t.Fatalf("failed to stat bundle script %s: %v", path1, err)
	}
	// Check restricted permissions (0600)
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected 0600 permissions, got %o", perm)
	}

	// 2. Subsequent call returns same cached path
	path2, err := EnsureBundleScript()
	if err != nil || path2 != path1 {
		t.Fatalf("expected matching cached path %s, got %s (err=%v)", path1, path2, err)
	}

	// 3. Cleanup removes the file
	if err := CleanupBundleScript(); err != nil {
		t.Fatalf("CleanupBundleScript failed: %v", err)
	}
	if _, err := os.Stat(path1); !os.IsNotExist(err) {
		t.Fatalf("expected bundle file to be removed after cleanup, stat err=%v", err)
	}

	// 4. Ensure recreation after cleanup
	path3, err := EnsureBundleScript()
	if err != nil || path3 == "" {
		t.Fatalf("EnsureBundleScript failed to recreate: %v", err)
	}
	if _, err := os.Stat(path3); err != nil {
		t.Fatalf("recreated bundle script not found: %v", err)
	}
}

func TestBoundedChallengeCache_SizeBounds(t *testing.T) {
	cache := NewBoundedChallengeCache(3)

	// Valid set & get
	cache.Set("key1", "val1")
	if v, ok := cache.Get("key1"); !ok || v != "val1" {
		t.Fatalf("expected val1, got %q", v)
	}

	// Oversized key rejection (> 4096 bytes)
	hugeKey := strings.Repeat("K", MaxCacheKeyLength+1)
	cache.Set(hugeKey, "val")
	if _, ok := cache.Get(hugeKey); ok {
		t.Fatal("expected oversized key to be rejected from cache")
	}

	// Oversized value rejection (> 2048 bytes)
	hugeVal := strings.Repeat("V", MaxCacheValueLength+1)
	cache.Set("key_oversized_val", hugeVal)
	if _, ok := cache.Get("key_oversized_val"); ok {
		t.Fatal("expected oversized value to be rejected from cache")
	}

	// Eviction at capacity
	cache.Set("key2", "val2")
	cache.Set("key3", "val3")
	cache.Set("key4", "val4") // evicts key1

	if _, ok := cache.Get("key1"); ok {
		t.Fatal("expected key1 to be evicted when cache exceeded capacity 3")
	}
	if v, ok := cache.Get("key4"); !ok || v != "val4" {
		t.Fatalf("expected key4 present, got %q", v)
	}
}

func TestSolver_NoSecretLeakage(t *testing.T) {
	solver := getTestSolver(t)

	secretToken := "SUPER_SECRET_CIPHER_VALUE_12345"
	corrupted := []byte("invalid script content")
	script := youtube.NewPlayerScript("https://www.youtube.com/s/player/bad/base.js", "bad", corrupted)

	batch := youtube.ChallengeBatch{
		Signatures: []youtube.SignatureChallenge{
			{ID: "sig-1", CipherString: secretToken, TargetParam: "sig"},
		},
	}

	res, err := solver.SolveChallenges(context.Background(), script, batch)
	if err != nil {
		if strings.Contains(err.Error(), secretToken) {
			t.Fatalf("top-level error leaked secret token: %s", err.Error())
		}
		return
	}

	sigRes := res.Signatures["sig-1"]
	if sigRes.Error != nil && strings.Contains(sigRes.Error.Error(), secretToken) {
		t.Fatalf("signature result error leaked secret token: %s", sigRes.Error.Error())
	}
}

func TestSolver_NodeBackend_ExplicitExecution(t *testing.T) {
	_, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("GOYT_REQUIRE_NODE_RUNTIME") != "" {
			t.Fatalf("expected Node.js in CI environment, but not found: %v", err)
		}
		t.Skip("skipping Node backend test: node not found on host")
	}

	nodeSolver, err := New(WithRuntime(RuntimeNode))
	if err != nil {
		t.Fatalf("failed to initialize Node solver: %v", err)
	}

	playerGB := loadFixture(t, "player_7460dd14_en_GB.js")
	scriptGB := youtube.NewPlayerScript("https://www.youtube.com/s/player/7460dd14/base.js", "7460dd14_en_GB", playerGB)

	batch := youtube.ChallengeBatch{
		Signatures: []youtube.SignatureChallenge{
			{ID: "sig-node", CipherString: "ABCD1234EFGH5678", TargetParam: "sig"},
		},
		NParams: []youtube.NChallenge{
			{ID: "n-node", RawValue: "M4F03qQkE9n8wA"},
		},
	}

	result, err := nodeSolver.SolveChallenges(context.Background(), scriptGB, batch)
	if err != nil {
		t.Fatalf("Node SolveChallenges error: %v", err)
	}

	wantSig := "65HGFE4"
	wantN := "7vBb38VB1P"

	if res, ok := result.Signatures["sig-node"]; !ok || res.Error != nil || res.Deciphered != wantSig {
		t.Errorf("Node backend sig got %q (err=%v), want %q", res.Deciphered, res.Error, wantSig)
	}
	if res, ok := result.NParams["n-node"]; !ok || res.Error != nil || res.Transformed != wantN {
		t.Errorf("Node backend n got %q (err=%v), want %q", res.Transformed, res.Error, wantN)
	}
}

func TestSolverBundle_EmbeddedDigestVerification(t *testing.T) {
	const expectedSHA256 = "ca150f7905cca3c13275b1e15e5073ef50278e96ed153372e5826973574ba483"

	// 1. Embedded bundle content must match the immutable pinned digest (integrity verification)
	h := sha256.Sum256([]byte(solverBundleJS))
	actualSHA256 := hex.EncodeToString(h[:])

	if actualSHA256 != expectedSHA256 {
		t.Fatalf("embedded solver bundle SHA-256 mismatch:\n  got:  %s\n  want: %s", actualSHA256, expectedSHA256)
	}

	// 2. Bundle written to filesystem on demand matches embedded bundle
	bundlePath, err := EnsureBundleScript()
	if err != nil {
		t.Fatalf("EnsureBundleScript error: %v", err)
	}

	fileData, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("failed to read written bundle file: %v", err)
	}

	fileHash := sha256.Sum256(fileData)
	actualFileSHA := hex.EncodeToString(fileHash[:])
	if actualFileSHA != expectedSHA256 {
		t.Fatalf("written bundle file SHA-256 mismatch:\n  got:  %s\n  want: %s", actualFileSHA, expectedSHA256)
	}
}
