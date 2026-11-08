package proctree

import (
	"os"
	"testing"

	"github.com/sachiniyer/agent-factory/internal/testsleeper"
)

func TestMain(m *testing.M) {
	testsleeper.Handle()
	os.Exit(m.Run())
}
