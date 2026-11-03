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
	var cleared int
	b := attentionClearingBinding{ptyTabBinding: nopTabBinding{},
		clear: func() { cleared++ }}
	for _, frame := range []string{"h", "i", "\x1b[I", "\x7f"} {
		if err := b.input([]byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	if cleared != 0 {
		t.Fatalf("keystrokes cleared attention: cleared=%d", cleared)
	}
	if err := b.input([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	if cleared != 1 {
		t.Fatalf("a submitted line: cleared=%d, want 1", cleared)
	}
}

func TestIsSubmittedInput(t *testing.T) {
	for in, want := range map[string]bool{"yes\r": true, "\r": true, "a": false, "\x1b[A": false, "\x1b[13;2u\r": false, "": false} {
		if got := isSubmittedInput([]byte(in)); got != want {
			t.Errorf("%q: got %v", in, got)
		}
	}
}
