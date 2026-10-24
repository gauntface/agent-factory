package daemon

import (
	"fmt"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
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

// PreviewSession is the public, read-only face of Preview: the agent tab's
// visible screen as plain text, cut to the last Lines non-trailing-blank lines.
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
	var snap PreviewResponse
	if err := s.Preview(PreviewRequest{Title: req.ID}, &snap); err != nil {
		return err
	}
	if snap.Gone {
		return fmt.Errorf("session %q is not running; restore it to preview it", req.ID)
	}
	resp.Text = lastLines(xansi.Strip(snap.Content), lines)
	return nil
}

func lastLines(text string, n int) string {
	all := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := range all {
		all[i] = strings.TrimRight(all[i], " \t\r")
	}
	end := len(all)
	for end > 0 && all[end-1] == "" {
		end--
	}
	start := max(0, end-n)
	return strings.Join(all[start:end], "\n")
}
