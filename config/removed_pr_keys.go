package config

import (
	"sync"

	"github.com/sachiniyer/agent-factory/log"
)

// Like the removed auto_yes setting, retired actions must not block upgrades
// or log a notice on every daemon config reload. Unknown actions still fail
// validation so typos in live bindings remain actionable.
var removedPRKeysWarned sync.Map

func discardRemovedPRKeyBindings(raw map[string]any, source string) {
	for _, action := range []string{"open_pr", "copy_pr"} {
		if _, present := raw[action]; !present {
			continue
		}
		delete(raw, action)
		key := struct{ source, action string }{source, action}
		if _, seen := removedPRKeysWarned.LoadOrStore(key, struct{}{}); !seen {
			log.WarningLog.Printf("config %s: keys.%s was removed; ignored", source, action)
		}
	}
}

// The backlog shipped a [backlog] table; a config that still has one must not
// log a notice on every reload.
var removedBacklogWarned sync.Map

func warnRemovedBacklogTable(source string) {
	if _, seen := removedBacklogWarned.LoadOrStore(source, struct{}{}); !seen {
		log.WarningLog.Printf("config %s: the backlog was removed; the [backlog] table is ignored and can be deleted", source)
	}
}
