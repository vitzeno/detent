package gate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePath(t *testing.T) {
	base := t.TempDir()

	allowedRoot := filepath.Join(base, "allowed")
	require.NoError(t, os.Mkdir(allowedRoot, 0o755))

	okFile := filepath.Join(allowedRoot, "ok.txt")
	require.NoError(t, os.WriteFile(okFile, []byte("ok"), 0o644))

	// A real file outside the allowed root, and a symlink inside the
	// allowed root that points to it — the classic escape.
	secretOutside := filepath.Join(base, "secret.txt")
	require.NoError(t, os.WriteFile(secretOutside, []byte("secret"), 0o644))
	escapeLink := filepath.Join(allowedRoot, "escape.txt")
	require.NoError(t, os.Symlink(secretOutside, escapeLink))

	// A symlink that stays inside the allowed root — proves symlinks
	// aren't rejected wholesale, only ones that resolve outside.
	realInside := filepath.Join(allowedRoot, "real.txt")
	require.NoError(t, os.WriteFile(realInside, []byte("real"), 0o644))
	linkInside := filepath.Join(allowedRoot, "link.txt")
	require.NoError(t, os.Symlink(realInside, linkInside))

	// A custom denied root nested inside an otherwise-allowed one, proving
	// the denied check is checked first and wins regardless.
	deniedSub := filepath.Join(base, "denied_sub")
	require.NoError(t, os.Mkdir(deniedSub, 0o755))
	deniedFile := filepath.Join(deniedSub, "classified.txt")
	require.NoError(t, os.WriteFile(deniedFile, []byte("classified"), 0o644))

	tests := []struct {
		name    string
		path    string
		rules   PathRules
		wantErr bool
	}{
		{
			name:  "file inside allowed root is accepted",
			path:  okFile,
			rules: PathRules{AllowedRoots: []string{allowedRoot}},
		},
		{
			name:    "adversarial: ../ traversal out of the allowed root",
			path:    filepath.Join(allowedRoot, "..", "secret.txt"),
			rules:   PathRules{AllowedRoots: []string{allowedRoot}},
			wantErr: true,
		},
		{
			name:    "adversarial: symlink inside the allowed root escaping outside it",
			path:    escapeLink,
			rules:   PathRules{AllowedRoots: []string{allowedRoot}},
			wantErr: true,
		},
		{
			name:  "symlink inside the allowed root pointing at another allowed file",
			path:  linkInside,
			rules: PathRules{AllowedRoots: []string{allowedRoot}},
		},
		{
			name:    "explicit denied root wins even nested inside an allowed root",
			path:    deniedFile,
			rules:   PathRules{AllowedRoots: []string{base}, DeniedRoots: []string{deniedSub}},
			wantErr: true,
		},
		{
			name:    "path outside every allowed root, no traversal syntax involved",
			path:    secretOutside,
			rules:   PathRules{AllowedRoots: []string{allowedRoot}},
			wantErr: true,
		},
		{
			name:    "nonexistent path is rejected",
			path:    filepath.Join(allowedRoot, "does-not-exist.txt"),
			rules:   PathRules{AllowedRoots: []string{allowedRoot}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := ValidatePath(tt.path, tt.rules)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.True(t, filepath.IsAbs(resolved))
		})
	}
}

func TestDefaultDeniedRoots(t *testing.T) {
	assert.Contains(t, DefaultDeniedRoots, "/etc")
	assert.Contains(t, DefaultDeniedRoots, "/sys")
	assert.Contains(t, DefaultDeniedRoots, "/dev")
}
