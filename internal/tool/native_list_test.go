package tool

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capture"
)

func TestListDir_NativePrintsAStableListing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no rwx bits to print")
	}
	dir := tree(t, map[string]string{"main.go": "package main\n", ".env": "X=1\n", "sub/x": "", "empty/.keep": ""})
	require.NoError(t, os.Remove(filepath.Join(dir, "empty", ".keep")))
	require.NoError(t, os.Symlink("main.go", filepath.Join(dir, "link")))
	when := time.Date(2026, 3, 4, 5, 6, 0, 0, time.Local)
	for _, name := range []string{"main.go", ".env", "sub", "empty"} {
		p := filepath.Join(dir, name)
		require.NoError(t, os.Chtimes(p, when, when))
		require.NoError(t, os.Chmod(p, map[bool]os.FileMode{true: 0o755, false: 0o644}[name == "sub" || name == "empty"]))
	}
	linkInfo, err := os.Lstat(filepath.Join(dir, "link"))
	require.NoError(t, err)
	link := fmt.Sprintf("%s   7  %s  link -> main.go\n", modeString(linkInfo.Mode()), linkInfo.ModTime().Format("2006-01-02 15:04"))
	t.Chdir(dir)

	assert.Equal(t, "drwxr-xr-x   -  2026-03-04 05:06  empty/\n"+
		link+
		"-rw-r--r--  13  2026-03-04 05:06  main.go\n"+
		"drwxr-xr-x   -  2026-03-04 05:06  sub/\n",
		ListDir{}.Run(t.Context(), Args{}).Stdout)

	all := ListDir{}.Run(t.Context(), Args{"all": true}).Stdout
	assert.True(t, strings.HasPrefix(all, "-rw-r--r--   4  2026-03-04 05:06  .env\n"), all)

	assert.Equal(t, "[the directory is empty]\n", ListDir{}.Run(t.Context(), Args{"path": "empty"}).Stdout)
	assert.Equal(t, "-rw-r--r--  13  2026-03-04 05:06  main.go\n", ListDir{}.Run(t.Context(), Args{"path": "main.go"}).Stdout)

	missing := ListDir{}.Run(t.Context(), Args{"path": "nope"})
	assert.Equal(t, 2, missing.ExitCode)
	assert.Equal(t, "list_dir: nope: no such file or directory\n", missing.Stderr)
}

// A name with a newline in it would otherwise read as two entries.
func TestListDir_NativeQuotesANameThatBreaksTheLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses the name")
	}
	t.Chdir(tree(t, map[string]string{"two\nlines": ""}))
	assert.Contains(t, ListDir{}.Run(t.Context(), Args{}).Stdout, `"two\nlines"`)
}

// searchAt points web_search at srv for one test.
func searchAt(t *testing.T, srv *httptest.Server) {
	t.Helper()
	was := readerPrefix
	readerPrefix = srv.URL + "/"
	t.Cleanup(func() { readerPrefix = was })
}

func TestWebSearch_NativeAsksWhatTheCommandWould(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte("1. A result\r\nhttps://example.com\n"))
	}))
	defer srv.Close()
	searchAt(t, srv)

	res := WebSearch{}.Run(t.Context(), Args{"query": "containerd rootless"})
	require.Equal(t, 0, res.ExitCode, res.Stderr)
	assert.Equal(t, "1. A result\nhttps://example.com\n", res.Stdout)
	require.NotNil(t, got)
	assert.Equal(t, "/"+searchBase+"containerd+rootless", got.URL.RequestURI())
	assert.Equal(t, "true", got.Header.Get("x-no-cache"))
}

// curl -f fails on an error status rather than printing the error page.
func TestWebSearch_NativeFailsOnAnErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	searchAt(t, srv)

	res := WebSearch{}.Run(t.Context(), Args{"query": "x"})
	assert.Equal(t, 22, res.ExitCode)
	assert.Empty(t, res.Stdout)
	assert.Contains(t, res.Stderr, "429")
}

func TestWebSearch_NativeCapsWhatItKeeps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a result line\n", 2000)))
	}))
	defer srv.Close()
	searchAt(t, srv)

	res := WebSearch{}.Run(t.Context(), Args{"query": "x"})
	assert.True(t, res.Truncated)
	assert.LessOrEqual(t, len(res.Stdout), capture.MaxOutputBytes)
}

func TestWebSearch_NativeRefusesWhatLowerRefuses(t *testing.T) {
	for _, q := range []string{"  ", strings.Repeat("q", maxQueryBytes+1)} {
		assert.NotZero(t, WebSearch{}.Run(t.Context(), Args{"query": q}).ExitCode)
	}
}
