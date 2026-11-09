package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	aflog "github.com/sachiniyer/agent-factory/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemovedBacklogTableWarnsOnce(t *testing.T) {
	cases := []struct {
		name, file, body string
	}{
		{"toml", TomlConfigFileName, "default_program = \"claude\"\n\n[backlog.prices.\"m1\"]\ninput = 1\noutput = 2\n\n[backlog.prices.\"m2\"]\ninput = 3\noutput = 4\n"},
		{"json", ConfigFileName, `{"default_program": "claude", "backlog": {"prices": {"m1": {"input": 1, "output": 2}}}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("AGENT_FACTORY_HOME", t.TempDir())
			configDir, err := GetConfigDir()
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(configDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(configDir, c.file), []byte(c.body), 0o644))

			buf := captureLog(t, &aflog.WarningLog)
			for range 2 {
				_, err := LoadConfig()
				require.NoError(t, err)
			}
			out := buf.String()
			assert.Equal(t, 1, strings.Count(out, "the backlog was removed"), out)
			assert.NotContains(t, out, `unknown key "backlog`)
		})
	}
}
