package ui

import (
	"os"
	"testing"
)

// Most tests answer a question the moment it goes up, so settling is off
// unless a test turns it back on.
func TestMain(m *testing.M) {
	questionSettle = 0
	os.Exit(m.Run())
}
