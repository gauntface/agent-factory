package daemon

import (
	"strings"
	"testing"

	"github.com/sachiniyer/agent-factory/config"
	"github.com/sachiniyer/agent-factory/internal/testguard"
)

func TestLastLines(t *testing.T) {
	cases := []struct {
		name, in string
		n        int
		want     string
	}{
		{"fewer than n", "a\nb", 5, "a\nb"},
		{"keeps the tail", "a\nb\nc\nd", 2, "c\nd"},
		{"drops trailing blank screen rows", "a\nb\n\n   \n\n", 2, "a\nb"},
		{"trims trailing spaces and CR", "a  \r\nb\t\r\n", 5, "a\nb"},
		{"keeps inner blank lines", "a\n\nb", 3, "a\n\nb"},
		{"empty", "", 3, ""},
	}
	for _, tc := range cases {
		if got := lastLines(tc.in, tc.n); got != tc.want {
			t.Errorf("%s: lastLines(%q, %d) = %q, want %q", tc.name, tc.in, tc.n, got, tc.want)
		}
	}
}

func TestPreviewSession_RejectsBadRequest(t *testing.T) {
	cs := &controlServer{}
	for _, req := range []PreviewSessionRequest{
		{ID: ""},
		{ID: "x", Lines: 41},
		{ID: "x", Lines: -1},
	} {
		var resp PreviewSessionResponse
		if err := cs.PreviewSession(req, &resp); err == nil {
			t.Errorf("PreviewSession(%+v) = nil error, want a refusal", req)
		}
	}
}

func TestPreviewSession_UnknownIDIsAnError(t *testing.T) {
	t.Setenv("AGENT_FACTORY_HOME", testguard.SocketTempDir(t))
	m, err := NewManager(config.DefaultConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	var resp PreviewSessionResponse
	err = (&controlServer{manager: m}).PreviewSession(PreviewSessionRequest{ID: "no-such-id", Lines: 12}, &resp)
	if err == nil || !strings.Contains(err.Error(), "no-such-id") {
		t.Fatalf("err = %v, want one that names the id", err)
	}
}

func TestPreviewSession_IsPublicAndNotSandboxed(t *testing.T) {
	for _, r := range HTTPRoutes() {
		if r.Path == "/v1/PreviewSession" {
			if sandboxAllowedPath(r.Path) {
				t.Fatal("/v1/PreviewSession must not be reachable with a sandbox credential")
			}
			return
		}
	}
	t.Fatal("/v1/PreviewSession is missing from the public af api catalog")
}
