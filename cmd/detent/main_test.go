package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/event"
)

// A script driving -prompt reads the exit status, not stdout.
func TestEndedError_GivesEachReasonItsOwnStatus(t *testing.T) {
	for reason, want := range map[event.EndReason]int{
		event.EndError: 1, event.EndAborted: 130, event.EndBound: 3,
	} {
		assert.Equal(t, want, endedError(reason).code(), reason)
	}
	assert.NotEmpty(t, endedError(event.EndError).Error())
	assert.Empty(t, endedError(event.EndAborted).Error(), "the reason is already printed")
}
