package session

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sachiniyer/agent-factory/internal/sessionenv"
)

func TestClaudeLaunchRunsThePlanTapAsItsStatusLine(t *testing.T) {
	t.Setenv("AGENT_FACTORY_HOME", t.TempDir())
	prev := afExecutable
	afExecutable = func() (string, error) { return "/opt/it's af/af", nil }
	t.Cleanup(func() { afExecutable = prev })

	command := injectSystemPrompt("claude", skillTarget{})
	_, ok := sessionenv.GeneratedArgsBetween("claude", command)
	require.True(t, ok, "the account boundary must be able to declare the generated words")
	// The shell, not a Go parser, says what claude receives.
	out, err := exec.Command("/bin/sh", "-c", "set -- "+strings.TrimPrefix(command, "claude ")+`; printf '%s\n%s' "$3" "$4"`).Output()
	require.NoError(t, err)
	flag, value, _ := strings.Cut(string(out), "\n")
	require.Equal(t, "--settings", flag)
	var settings struct {
		StatusLine struct {
			Type            string `json:"type"`
			Command         string `json:"command"`
			RefreshInterval int    `json:"refreshInterval"`
		} `json:"statusLine"`
	}
	require.NoError(t, json.Unmarshal([]byte(value), &settings))
	require.Equal(t, "command", settings.StatusLine.Type)
	require.Equal(t, `'/opt/it'\''s af/af' internal plan-tap`, settings.StatusLine.Command)
	require.Equal(t, 60, settings.StatusLine.RefreshInterval)

	afExecutable = func() (string, error) { return "", errors.New("no binary") }
	command = injectSystemPrompt("claude", skillTarget{})
	require.Contains(t, command, "--plugin-dir ")
	require.False(t, strings.Contains(command, "--settings"), command)
	require.NotContains(t, injectSystemPrompt("codex", skillTarget{}), "--settings")
}
