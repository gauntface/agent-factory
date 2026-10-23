package session

import (
	"encoding/json"
	"os"

	"github.com/sachiniyer/agent-factory/log"
	"github.com/sachiniyer/agent-factory/planusage"
)

// afExecutable is the af binary the status line tap runs. A var so tests can
// pin it.
var afExecutable = os.Executable

// planTapSettings is the --settings value that makes Claude Code run
// "af internal plan-tap" as its status line. --settings has a higher
// precedence than the user's settings, so the tap runs their status line
// itself. "" when af cannot name its own binary.
func planTapSettings() string {
	exe, err := afExecutable()
	if err != nil || exe == "" {
		log.WarningLog.Printf("plan usage: cannot locate the af binary for the status line tap: %v", err)
		return ""
	}
	data, err := json.Marshal(map[string]any{"statusLine": map[string]any{
		"type":            "command",
		"command":         shellQuote(exe) + " " + planusage.TapCommandArgs,
		"refreshInterval": 60,
	}})
	if err != nil {
		return ""
	}
	return string(data)
}
