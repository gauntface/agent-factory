package daemon

import (
	"testing"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/session"
)

func TestBacklogSessionState(t *testing.T) {
	for _, c := range []struct {
		v    session.LifecycleView
		want string
	}{
		{session.LifecycleView{Liveness: session.LiveRunning}, backlog.SessionLive},
		{session.LifecycleView{Liveness: session.LiveReady}, backlog.SessionLive},
		{session.LifecycleView{Liveness: session.LiveArchived}, backlog.SessionArchived},
		{session.LifecycleView{Liveness: session.LiveDead}, backlog.SessionDead},
		{session.LifecycleView{Liveness: session.LiveLost}, backlog.SessionLost},
		{session.LifecycleView{Liveness: session.LiveLimitReached}, backlog.SessionLimited},
		{session.LifecycleView{Liveness: session.LiveRunning, UserKilled: true}, backlog.SessionGone},
		{session.LifecycleView{Liveness: session.LiveRunning, InFlightOp: session.OpKilling}, backlog.SessionGone},
	} {
		if got := backlogSessionState(c.v); got != c.want {
			t.Errorf("%+v: %q, want %q", c.v, got, c.want)
		}
	}
}
