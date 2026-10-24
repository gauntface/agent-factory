package daemon

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/sachiniyer/agent-factory/session"
	"github.com/sachiniyer/agent-factory/session/tmux"
)

const maxPreviewSessionLines = 40

// PreviewSessionRequest asks for the last Lines lines of a session's agent tab.
// Lines 0 means maxPreviewSessionLines.
type PreviewSessionRequest struct {
	ID    string `json:"id"`
	Lines int    `json:"lines"`
}

type PreviewSessionResponse struct {
	Text string `json:"text"`
}

// PreviewSession is the public, read-only short form of Preview: the agent tab's
// visible screen as plain text. It resolves by stable id against the tracked
// instance map only, so an unknown id from a remote caller never costs a disk scan.
func (s *controlServer) PreviewSession(req PreviewSessionRequest, resp *PreviewSessionResponse) error {
	if req.ID == "" {
		return fmt.Errorf("id is required")
	}
	lines := req.Lines
	if lines == 0 {
		lines = maxPreviewSessionLines
	}
	if lines < 0 || lines > maxPreviewSessionLines {
		return fmt.Errorf("lines must be between 1 and %d, got %d", maxPreviewSessionLines, req.Lines)
	}
	if err := s.requireManagerReady(); err != nil {
		return err
	}
	m := s.manager
	m.mu.Lock()
	instance, repoID, title := m.trackedStreamSessionLocked(authoritativeStreamTarget(req.ID, ""))
	_, killing := m.killsInFlight[daemonInstanceKey(repoID, title)]
	m.mu.Unlock()
	if instance == nil {
		return fmt.Errorf("session %q not found; pass the session id, not its title", req.ID)
	}
	if killing {
		return fmt.Errorf("session %q is being deleted", req.ID)
	}
	if instance.IsArchived() {
		return fmt.Errorf("session %q is archived; restore it to preview it", req.ID)
	}
	if !instance.Started() {
		return fmt.Errorf("session %q is not running; restore or start it to preview it", req.ID)
	}
	tabID := ""
	for _, t := range instance.GetTabs() {
		if t.Kind == session.TabKindAgent {
			tabID = t.ID
			break
		}
	}
	if tabID == "" {
		return fmt.Errorf("session %q has no agent tab to preview", req.ID)
	}
	snap, err := instance.AgentServer().PreviewByID(tabID, false)
	if err != nil {
		return previewSessionErr(req.ID, err)
	}
	resp.Text = lastLines(plainText(snap.Content), lines)
	return nil
}

func previewSessionErr(id string, err error) error {
	switch {
	case errors.Is(err, session.ErrTabGone):
		return fmt.Errorf("session %q agent tab closed during the preview; try again", id)
	case errors.Is(err, tmux.ErrSessionGone):
		return fmt.Errorf("session %q is not running; restore it to preview it", id)
	}
	return err
}

// plainText strips escape sequences, then the control bytes xansi.Strip keeps
// (C0, DEL, C1) and invalid UTF-8 such as a raw 8-bit CSI.
func plainText(s string) string {
	s = xansi.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if r == utf8.RuneError && size == 1 {
			continue
		}
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func lastLines(text string, n int) string {
	all := strings.Split(text, "\n")
	for i := range all {
		all[i] = strings.TrimRight(all[i], " \t")
	}
	end := len(all)
	for end > 0 && all[end-1] == "" {
		end--
	}
	start := max(0, end-n)
	return strings.Join(all[start:end], "\n")
}
