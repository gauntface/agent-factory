// Package testsleeper starts test processes that only sleep, under any file
// name or argv[0]. A renamed copy of sleep(1) does not work where sleep is a
// multi-call coreutils (Ubuntu 26.04): it picks the utility from argv[0] and
// exits. The package imports nothing from this module, so any package's tests
// can use it without an import cycle.
package testsleeper

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// Arg is the argument that makes a test binary sleep instead of running tests.
const Arg = "--af-test-sleeper"

// Handle sleeps and exits when this binary was started with Arg. Call it first
// in TestMain.
func Handle() {
	if len(os.Args) != 2 || os.Args[1] != Arg {
		return
	}
	time.Sleep(300 * time.Second)
	os.Exit(0)
}

// Command runs this test binary as a sleeper. The caller may change Args[0].
func Command(t testing.TB) *exec.Cmd {
	t.Helper()
	return exec.Command(self(t), Arg)
}

// CopyCommand copies this test binary to path and runs the copy as a sleeper,
// so the process's kernel name is the base name of path.
func CopyCommand(t testing.TB, path string) *exec.Cmd {
	t.Helper()
	body, err := os.ReadFile(self(t))
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatalf("copy test binary: %v", err)
	}
	return exec.Command(path, Arg)
}

func self(t testing.TB) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("find test binary: %v", err)
	}
	return path
}
