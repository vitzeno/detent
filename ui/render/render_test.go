package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiffClass(t *testing.T) {
	assert.Equal(t, "add", DiffClass("+added"))
	assert.Equal(t, "del", DiffClass("-removed"))
	assert.Equal(t, "hunk", DiffClass("@@ -1 +1 @@"))
	assert.Equal(t, "meta", DiffClass("+++ b/f"))
	assert.Equal(t, "ctx", DiffClass(" context"))
}
