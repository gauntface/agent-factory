package proctree

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const sleeperArg = "--proctree-test-sleeper"

func TestMain(m *testing.M) {
	handleSleeperExec()
	os.Exit(m.Run())
}

func handleSleeperExec() {
	if len(os.Args) == 2 && os.Args[1] == sleeperArg {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
}

// copyTestBinary copies this test binary to path; run it with sleeperArg to get
// a process that only sleeps under that file name. A renamed copy of sleep(1)
// does not work where sleep is a multi-call coreutils (Ubuntu 26.04), which
// picks the utility from argv[0] and exits.
func copyTestBinary(t *testing.T, path string) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	body, err := os.ReadFile(self)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o755))
}
