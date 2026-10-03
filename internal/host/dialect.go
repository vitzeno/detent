package host

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

// The shells a host command can run in, as host_shell names them.
const (
	Sh      = "sh"
	Pwsh    = "pwsh"
	GitBash = "gitbash"
)

// Dialects are every host_shell value bar the empty one, which picks per OS.
var Dialects = []string{Sh, Pwsh, GitBash}

// errNoShell is Windows with neither shell installed.
var errNoShell = errors.New("no shell to run commands in: install PowerShell 7 (winget install Microsoft.PowerShell) " +
	"or Git for Windows (winget install Git.Git), or name one with host_shell")

// Find builds the Shell for dialect, empty for this OS's own: sh, or on
// Windows PowerShell 7 and then Git Bash.
func Find(dialect string, opts ...Option) (*Shell, error) {
	d, path, err := thisSystem().find(dialect)
	if err != nil {
		return nil, err
	}
	s := NewShell(opts...)
	s.dialect, s.path = d, path
	return s, nil
}

// system is what finding a shell asks of the machine, so a test can be Windows.
type system struct {
	goos        string
	lookPath    func(string) (string, error)
	getenv      func(string) string
	exists      func(string) bool
	gitExecPath func() string
}

func thisSystem() system {
	return system{
		goos: runtime.GOOS, lookPath: exec.LookPath, getenv: os.Getenv,
		exists: func(p string) bool {
			info, err := os.Stat(p)
			return err == nil && !info.IsDir()
		},
		gitExecPath: gitExecPath,
	}
}

func (sys system) find(dialect string) (string, string, error) {
	switch dialect {
	case "":
		if sys.goos != "windows" {
			return sys.find(Sh)
		}
		for _, d := range []string{Pwsh, GitBash} {
			if _, path, err := sys.find(d); err == nil {
				return d, path, nil
			}
		}
		return "", "", errNoShell
	case Sh:
		path, err := sys.lookPath("sh")
		if err != nil {
			return "", "", errors.New("host_shell sh: there is no sh on PATH")
		}
		return Sh, path, nil
	case Pwsh:
		path, err := sys.lookPath("pwsh")
		if err != nil {
			return "", "", errors.New("host_shell pwsh: PowerShell 7 is not on PATH, install it with winget install Microsoft.PowerShell")
		}
		return Pwsh, path, nil
	case GitBash:
		if path := sys.gitBash(); path != "" {
			return GitBash, path, nil
		}
		return "", "", errors.New("host_shell gitbash: Git Bash was not found, install Git for Windows with winget install Git.Git")
	}
	return "", "", fmt.Errorf("unknown host_shell %q, choose one of: %s", dialect, strings.Join(Dialects, ", "))
}

// gitBash is Git for Windows' bin\bash.exe, which sets up PATH as usr\bin\bash.exe
// does not, and never WSL's bash.exe in System32.
func (sys system) gitBash() string {
	if sys.goos != "windows" {
		path, _ := sys.lookPath("bash")
		return path
	}
	// git --exec-path is <root>/mingw64/libexec/git-core, a few levels under the install.
	if dir := sys.gitExecPath(); dir != "" {
		for range 4 {
			dir = filepath.Dir(dir)
			if p := filepath.Join(dir, "bin", "bash.exe"); sys.exists(p) {
				return p
			}
		}
	}
	// Any bash.exe under a git directory, which may be usr\bin's.
	if p, err := sys.lookPath("bash.exe"); err == nil && strings.Contains(strings.ToLower(filepath.ToSlash(p)), "/git/") {
		return p
	}
	for _, root := range []string{sys.getenv("ProgramFiles"), sys.getenv("ProgramW6432"), sys.getenv("ProgramFiles(x86)")} {
		if p := filepath.Join(root, "Git", "bin", "bash.exe"); root != "" && sys.exists(p) {
			return p
		}
	}
	if local := sys.getenv("LocalAppData"); local != "" {
		if p := filepath.Join(local, "Programs", "Git", "bin", "bash.exe"); sys.exists(p) {
			return p
		}
	}
	return ""
}

func gitExecPath() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "--exec-path").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// argv is the program and arguments that run command in this dialect.
func (s *Shell) argv(command string) ([]string, error) {
	switch s.Dialect() {
	case Pwsh:
		args, err := pwshArgs(command)
		if err != nil {
			return nil, err
		}
		return append([]string{s.path}, args...), nil
	case GitBash:
		return []string{s.path, "-c", command}, nil
	}
	return []string{cmp.Or(s.path, "sh"), "-c", command}, nil
}

// pwshPrelude makes output UTF-8 and keeps progress bars out of the capture.
// It goes first, so a script that opens with a using statement fails to parse.
const pwshPrelude = "[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)\n" +
	"$ProgressPreference = 'SilentlyContinue'\n"

// pwshStatus exits 0 when the last statement worked, else the last native exit
// code, which may be an earlier statement's, else 1. $? is read first, as anything resets it.
const pwshStatus = "\n$detentOK = $?\n" +
	"if ($detentOK) { exit 0 }\n" +
	"if ($LASTEXITCODE) { exit $LASTEXITCODE }\n" +
	"exit 1\n"

// pwshMaxEncoded leaves room in Windows' 32767 character command line
// for the program and its flags.
const pwshMaxEncoded = 32000

// pwshArgs runs script encoded, since quoting through -Command breaks on
// the first nested quote once Windows has re-parsed the command line.
func pwshArgs(script string) ([]string, error) {
	enc := encodeUTF16(pwshPrelude + script + pwshStatus)
	if len(enc) > pwshMaxEncoded {
		return nil, fmt.Errorf("host: command too long for PowerShell's command line (%d bytes encoded, the limit is %d)", len(enc), pwshMaxEncoded)
	}
	return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", enc}, nil
}

// encodeUTF16 is what -EncodedCommand takes: base64 of UTF-16LE.
func encodeUTF16(s string) string {
	units := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(b[2*i:], u)
	}
	return base64.StdEncoding.EncodeToString(b)
}
