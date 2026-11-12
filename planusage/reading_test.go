package planusage

import (
	"path/filepath"
	"testing"
)

func TestReadingAccounts(t *testing.T) {
	dir := filepath.Join(home(t), DirName)
	if got, err := ReadingAccounts(); err != nil || got != nil {
		t.Fatalf("no plan dir: got %v, %v", got, err)
	}
	write(t, filepath.Join(dir, "default.json"), "{}")
	write(t, filepath.Join(dir, "work.json"), "{}")
	write(t, filepath.Join(dir, "notes.txt"), "x")
	write(t, filepath.Join(dir, "bad name.json"), "{}")
	write(t, filepath.Join(dir, "sub.json", "x"), "{}")
	got, err := ReadingAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "default" || got[1] != "work" {
		t.Fatalf("got %v, want [default work]", got)
	}
}
