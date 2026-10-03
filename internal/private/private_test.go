package private

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIs(t *testing.T) {
	for mode, want := range map[os.FileMode]bool{0o600: true, 0o400: true, 0o640: false, 0o604: false, 0o666: false} {
		path := filepath.Join(t.TempDir(), "f")
		require.NoError(t, os.WriteFile(path, nil, 0o600))
		require.NoError(t, os.Chmod(path, mode))
		info, err := os.Stat(path)
		require.NoError(t, err)
		if runtime.GOOS == "windows" {
			want = true
		}
		assert.Equal(t, want, Is(info), "%o", mode)
	}
}
