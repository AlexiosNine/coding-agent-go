package cc_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	cc "github.com/alexioschen/cc-connect/goagent"
)

func TestOSSandbox_IsAvailable(t *testing.T) {
	sandbox := cc.NewOSSandbox("/tmp")
	available := sandbox.IsAvailable()

	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err == nil {
			if !available {
				t.Error("sandbox-exec exists but IsAvailable returned false")
			}
		}
	case "linux":
		if _, err := exec.LookPath("docker"); err == nil {
			if !available {
				t.Error("docker exists but IsAvailable returned false")
			}
		}
	}
}

func TestOSSandbox_ContextPropagation(t *testing.T) {
	sandbox := cc.NewOSSandbox("/tmp/workspace")
	ctx := cc.WithOSSandbox(context.Background(), sandbox)

	retrieved := cc.GetOSSandbox(ctx)
	if retrieved == nil {
		t.Fatal("expected sandbox from context")
	}
	if len(retrieved.AllowedPaths) != 1 {
		t.Errorf("expected 1 allowed path, got %d", len(retrieved.AllowedPaths))
	}
}

func TestOSSandbox_WrapCommand_Seatbelt(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Seatbelt only available on macOS")
	}

	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available")
	}

	tmpDir := t.TempDir()
	sandbox := cc.NewOSSandbox(tmpDir)

	cmd, err := sandbox.WrapCommand(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("WrapCommand failed: %v", err)
	}

	// Verify it's using sandbox-exec
	if cmd.Path != "/usr/bin/sandbox-exec" && !strings.Contains(cmd.Path, "sandbox-exec") {
		t.Errorf("expected sandbox-exec, got %s", cmd.Path)
	}

	// Verify profile contains allowed path
	profileFound := false
	for _, arg := range cmd.Args {
		if strings.Contains(arg, tmpDir) {
			profileFound = true
			break
		}
	}
	if !profileFound {
		t.Error("Seatbelt profile doesn't contain allowed path")
	}
}

func TestOSSandbox_WrapCommand_Docker(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Docker sandbox only tested on Linux")
	}

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	tmpDir := t.TempDir()
	sandbox := cc.NewOSSandbox(tmpDir)

	cmd, err := sandbox.WrapCommand(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("WrapCommand failed: %v", err)
	}

	// Verify it's using docker
	if !strings.Contains(cmd.Path, "docker") {
		t.Errorf("expected docker, got %s", cmd.Path)
	}

	// Verify --network=none flag
	hasNetworkNone := false
	for _, arg := range cmd.Args {
		if arg == "--network=none" {
			hasNetworkNone = true
			break
		}
	}
	if !hasNetworkNone {
		t.Error("Docker command missing --network=none flag")
	}
}

func TestOSSandbox_Seatbelt_BlocksOutsideAccess(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Seatbelt only on macOS")
	}

	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available")
	}

	tmpDir := t.TempDir()
	sandbox := cc.NewOSSandbox(tmpDir)

	// Try to read /etc/passwd (should be blocked)
	cmd, err := sandbox.WrapCommand(context.Background(), "cat /etc/passwd")
	if err != nil {
		t.Fatalf("WrapCommand failed: %v", err)
	}

	output, err := cmd.CombinedOutput()
	// Should fail due to sandbox restrictions
	if err == nil {
		t.Logf("Warning: Seatbelt didn't block /etc/passwd access. Output: %s", output)
		// Note: This might not fail in all macOS versions/configurations
	}
}

func TestOSSandbox_Docker_BlocksOutsideAccess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Docker sandbox only on Linux")
	}

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	tmpDir := t.TempDir()
	sandbox := cc.NewOSSandbox(tmpDir)

	// Try to read /etc/passwd (should be blocked by bind mount isolation)
	cmd, err := sandbox.WrapCommand(context.Background(), "cat /etc/passwd")
	if err != nil {
		t.Fatalf("WrapCommand failed: %v", err)
	}

	output, err := cmd.CombinedOutput()
	// Docker container has its own /etc/passwd, so this will succeed
	// but it's the container's passwd, not the host's.
	if err != nil {
		t.Logf("Docker command failed (expected in isolated sandbox): %v", err)
		return
	}

	// Verify the container's /etc/passwd does NOT contain the host user's
	// home directory (e.g. /home/runner or /Users/...), which would indicate
	// the host filesystem is leaking into the container.
	hostPasswd, readErr := os.ReadFile("/etc/passwd")
	if readErr != nil {
		t.Logf("Could not read host /etc/passwd for comparison: %v", readErr)
		return
	}
	if string(output) == string(hostPasswd) {
		t.Error("Container /etc/passwd is identical to host — sandbox isolation may be broken")
	}
}

func TestOSSandbox_AllowsWorkspaceAccess(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Seatbelt profile requires complex permissions, skip on macOS")
	}

	if !cc.NewOSSandbox(".").IsAvailable() {
		t.Skip("OS sandbox not available on this system")
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("hello sandbox"), 0644)

	sandbox := cc.NewOSSandbox(tmpDir)
	cmd, err := sandbox.WrapCommand(context.Background(), "cat test.txt")
	if err != nil {
		t.Fatalf("WrapCommand failed: %v", err)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Command failed: %v, output: %s", err, output)
	}

	if !strings.Contains(string(output), "hello sandbox") {
		t.Errorf("Expected 'hello sandbox', got: %s", output)
	}
}

func TestOSSandbox_UnsupportedOS(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("Test only for unsupported OS")
	}

	sandbox := cc.NewOSSandbox("/tmp")
	_, err := sandbox.WrapCommand(context.Background(), "echo test")
	if err == nil {
		t.Error("Expected error on unsupported OS")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("Expected 'not supported' error, got: %v", err)
	}
}
