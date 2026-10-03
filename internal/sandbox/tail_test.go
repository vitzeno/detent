package sandbox

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capture"
)

func TestTailFile(t *testing.T) {
	cases := []struct {
		name  string
		write func(t *testing.T, path string, done chan struct{})
		want  string
	}{
		{name: "a file that appears late", write: func(t *testing.T, path string, done chan struct{}) {
			t.Helper()
			time.Sleep(100 * time.Millisecond)
			assert.NoError(t, os.WriteFile(path, []byte("late\n"), 0o644)) //nolint:testifylint // runs off the test goroutine
			time.Sleep(50 * time.Millisecond)
			close(done)
		}, want: "late\n"},
		{name: "what lands just before done is drained", write: func(t *testing.T, path string, done chan struct{}) {
			t.Helper()
			assert.NoError(t, os.WriteFile(path, []byte("a\n"), 0o644)) //nolint:testifylint // runs off the test goroutine
			appendTo(t, path, "b\n")
			close(done)
		}, want: "a\nb\n"},
		{name: "a line split across polls", write: func(t *testing.T, path string, done chan struct{}) {
			t.Helper()
			assert.NoError(t, os.WriteFile(path, []byte("hal"), 0o644)) //nolint:testifylint // runs off the test goroutine
			time.Sleep(3 * tailPollInterval)
			appendTo(t, path, "f\n")
			time.Sleep(3 * tailPollInterval)
			close(done)
		}, want: "half\n"},
		{name: "a file that never appears", write: func(_ *testing.T, _ string, done chan struct{}) {
			close(done)
		}, want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "out")
			done := make(chan struct{})
			go c.write(t, path, done)
			var buf bytes.Buffer
			tailFile(path, false, &buf, capture.MaxOutputBytes, nil, done)
			assert.Equal(t, c.want, buf.String())
		})
	}
}

func TestHeldElsewhere(t *testing.T) {
	host, err := os.Hostname()
	require.NoError(t, err)
	cases := []struct {
		label string
		want  bool
	}{
		{"", false},
		{"garbage", false},
		{strconv.Itoa(os.Getppid()) + "@" + host, true},
		{strconv.Itoa(os.Getppid()) + "@another-machine", false},
		{strconv.Itoa(os.Getpid()) + "@" + host, false},
		{"0@" + host, false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, heldElsewhere(c.label), "label %q", c.label)
	}
}

func TestOutputDir_IgnoresItselfAndGoesWhenEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), sandboxOutputDir)
	require.NoError(t, writeIgnore(dir))
	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "*\n", string(got))

	require.NoError(t, os.Mkdir(filepath.Join(dir, "other-session"), 0o755))
	removeOutputDir(dir)
	assert.DirExists(t, dir, "another session still uses it")

	require.NoError(t, os.Remove(filepath.Join(dir, "other-session")))
	removeOutputDir(dir)
	assert.NoDirExists(t, dir)
}

func TestStart_RefusesWhatItCannotHonour(t *testing.T) {
	cases := map[string]Option{
		"an unknown network": WithNetwork("bridge"),
		"a zero limit":       WithLimit(0),
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			err := NewContainer(opt).Start(context.Background(), "never")
			assert.Error(t, err)
		})
	}
}

func TestWithReadOnly_CopiesTheMap(t *testing.T) {
	dirs := map[string]string{"/a": "/b"}
	c := NewContainer(WithReadOnly(dirs))
	dirs["/c"] = "/d"
	assert.Len(t, c.readOnly, 1)
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if !assert.NoError(t, err) { //nolint:testifylint // called off the test goroutine
		return
	}
	_, err = f.WriteString(s)
	assert.NoError(t, err)
	assert.NoError(t, f.Close())
}
