package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatePID(t *testing.T) {
	known := []ProcessInfo{
		{PID: 4821, Owner: "alice"},
		{PID: 5140, Owner: "root"},
	}

	tests := []struct {
		name        string
		pid         int
		known       []ProcessInfo
		currentUser string
		wantErr     bool
	}{
		{name: "owned pid in known list", pid: 4821, known: known, currentUser: "alice", wantErr: false},
		{name: "adversarial: pid 1, even if somehow in the known list",
			pid: 1, known: append(known, ProcessInfo{PID: 1, Owner: "alice"}), currentUser: "alice", wantErr: true},
		{name: "adversarial: pid not in fetched process list", pid: 9999, known: known, currentUser: "alice", wantErr: true},
		{name: "adversarial: pid owned by a different user", pid: 5140, known: known, currentUser: "alice", wantErr: true},
		{name: "empty process list", pid: 4821, known: nil, currentUser: "alice", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePID(tt.pid, tt.known, tt.currentUser)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
