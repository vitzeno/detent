package welcome

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
)

// The declaration and RefreshStyles list the styles separately, so a
// refresh under the same theme proves they list them in the same order.
func TestRefreshStyles_KeepsEachStyleInItsPlace(t *testing.T) {
	was := []lipgloss.Style{brand, primary, muted, faint, safe, caution, hint}
	RefreshStyles()
	now := []lipgloss.Style{brand, primary, muted, faint, safe, caution, hint}
	assert.Equal(t, was, now)
}
