package daemon

import "testing"

func TestIsTypedInputIgnoresTerminalReplies(t *testing.T) {
	cases := map[string]bool{
		"a":          true,
		"\r":         true,
		"\x03":       true,
		"\x1b[I":     false,
		"\x1b[O":     false,
		"\x1b[12;1R": false,
		"":           false,
	}
	for in, want := range cases {
		if got := isTypedInput([]byte(in)); got != want {
			t.Errorf("isTypedInput(%q) = %v, want %v", in, got, want)
		}
	}
}

type nopTabBinding struct{ ptyTabBinding }

func (nopTabBinding) input([]byte) error { return nil }

func TestAttentionClearsOnlyOnSubmittedLine(t *testing.T) {
	var cleared, submitted int
	b := attentionClearingBinding{ptyTabBinding: nopTabBinding{},
		clear: func() { cleared++ }, submitted: func() { submitted++ }}
	for _, frame := range []string{"h", "i", "\x1b[I", "\x7f"} {
		if err := b.input([]byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	if cleared != 0 || submitted != 0 {
		t.Fatalf("keystrokes cleared attention: cleared=%d submitted=%d", cleared, submitted)
	}
	if err := b.input([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	if cleared != 1 || submitted != 1 {
		t.Fatalf("a submitted line: cleared=%d submitted=%d, want 1 and 1", cleared, submitted)
	}
}
