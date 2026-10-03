package host

import (
	"encoding/base64"
	"encoding/binary"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSystem is a machine with exactly the programs and files named.
func fakeSystem(goos string, onPath map[string]string, files ...string) system {
	return system{
		goos: goos,
		lookPath: func(name string) (string, error) {
			if p, ok := onPath[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		},
		getenv: func(k string) string {
			return map[string]string{"ProgramFiles": "C:/Program Files", "LocalAppData": "C:/Users/me/AppData/Local"}[k]
		},
		exists: func(p string) bool {
			for _, f := range files {
				if filepath.FromSlash(f) == p {
					return true
				}
			}
			return false
		},
		gitExecPath: func() string { return onPath["git --exec-path"] },
	}
}

func TestFind_PicksAShellPerOS(t *testing.T) {
	gitBash := filepath.Join("C:/Program Files", "Git", "bin", "bash.exe")
	tests := []struct {
		name     string
		sys      system
		dialect  string
		want     string
		wantPath string
		wantErr  string
	}{
		{name: "sh off Windows", sys: fakeSystem("linux", map[string]string{"sh": "/bin/sh", "pwsh": "/usr/bin/pwsh"}),
			want: Sh, wantPath: "/bin/sh"},
		{name: "pwsh first on Windows", sys: fakeSystem("windows", map[string]string{"pwsh": `C:\pwsh.exe`}, "C:/Program Files/Git/bin/bash.exe"),
			want: Pwsh, wantPath: `C:\pwsh.exe`},
		{name: "then Git Bash where git says it is",
			sys:  fakeSystem("windows", map[string]string{"git --exec-path": "D:/Tools/Git/mingw64/libexec/git-core"}, "D:/Tools/Git/bin/bash.exe"),
			want: GitBash, wantPath: filepath.Join("D:/Tools/Git", "bin", "bash.exe")},
		{name: "then Git Bash under Program Files", sys: fakeSystem("windows", nil, "C:/Program Files/Git/bin/bash.exe"),
			want: GitBash, wantPath: gitBash},
		{name: "then Git Bash on PATH", sys: fakeSystem("windows", map[string]string{"bash.exe": `C:/Git/usr/bin/bash.exe`}),
			want: GitBash, wantPath: `C:/Git/usr/bin/bash.exe`},
		{name: "never WSL's bash", sys: fakeSystem("windows", map[string]string{"bash.exe": `C:/Windows/System32/bash.exe`}),
			wantErr: "winget install Microsoft.PowerShell"},
		{name: "a user install of Git", sys: fakeSystem("windows", nil, "C:/Users/me/AppData/Local/Programs/Git/bin/bash.exe"),
			want: GitBash, wantPath: filepath.Join("C:/Users/me/AppData/Local", "Programs", "Git", "bin", "bash.exe")},
		{name: "neither on Windows names both installs", sys: fakeSystem("windows", nil),
			wantErr: "Git for Windows"},
		{name: "pwsh asked for and absent", sys: fakeSystem("darwin", nil), dialect: Pwsh,
			wantErr: "PowerShell 7 is not on PATH"},
		{name: "pwsh asked for off Windows", sys: fakeSystem("darwin", map[string]string{"pwsh": "/opt/pwsh"}), dialect: Pwsh,
			want: Pwsh, wantPath: "/opt/pwsh"},
		{name: "gitbash off Windows is bash", sys: fakeSystem("linux", map[string]string{"bash": "/bin/bash"}), dialect: GitBash,
			want: GitBash, wantPath: "/bin/bash"},
		{name: "an unknown name", sys: fakeSystem("linux", nil), dialect: "cmd", wantErr: "choose one of: sh, pwsh, gitbash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, path, err := tt.sys.find(tt.dialect)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantPath, path)
		})
	}
}

func decodeUTF16(t *testing.T, enc string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(enc)
	require.NoError(t, err)
	require.Zero(t, len(raw)%2)
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	return string(utf16.Decode(units))
}

func TestPwshArgs_EncodesThePreludeTheScriptAndTheStatus(t *testing.T) {
	script := `Write-Output "naïve 'quotes' ✓ 𝄞"`
	args, err := pwshArgs(script)
	require.NoError(t, err)
	require.Len(t, args, 5)
	assert.Equal(t, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand"}, args[:4])

	got := decodeUTF16(t, args[4])
	assert.Equal(t, pwshPrelude+script+pwshStatus, got, "byte for byte, astral runes included")
	assert.Contains(t, got, "[Text.UTF8Encoding]::new($false)", "UTF-8 without a BOM")
	assert.Contains(t, got, "$ProgressPreference = 'SilentlyContinue'")
	// A trailing comment must not swallow the status line.
	assert.True(t, strings.HasPrefix(pwshStatus, "\n"))
}

func TestPwshArgs_RefusesWhatTheCommandLineCannotHold(t *testing.T) {
	_, err := pwshArgs(strings.Repeat("x", 20000))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too long")
}

func TestShell_ArgvPerDialect(t *testing.T) {
	sh, err := (&Shell{}).argv("echo hi")
	require.NoError(t, err)
	assert.Equal(t, []string{"sh", "-c", "echo hi"}, sh)

	bash, err := (&Shell{dialect: GitBash, path: `C:\Git\bin\bash.exe`}).argv("echo hi")
	require.NoError(t, err)
	assert.Equal(t, []string{`C:\Git\bin\bash.exe`, "-c", "echo hi"}, bash)

	ps, err := (&Shell{dialect: Pwsh, path: "pwsh"}).argv("echo hi")
	require.NoError(t, err)
	assert.Equal(t, "pwsh", ps[0])
	assert.Equal(t, "-EncodedCommand", ps[4])
}

func TestShell_DialectDefaultsToSh(t *testing.T) {
	assert.Equal(t, Sh, NewShell().Dialect())
}

// pwshShell runs the real thing, wherever PowerShell 7 is installed.
func pwshShell(t *testing.T) *Shell {
	t.Helper()
	s, err := Find(Pwsh)
	if err != nil {
		t.Skip("PowerShell 7 is not installed")
	}
	return s
}

func TestPwsh_ExitStatusMatchesSh(t *testing.T) {
	s := pwshShell(t)
	tests := []struct {
		name   string
		script string
		code   int
		stdout string
	}{
		{name: "a cmdlet that works", script: "Write-Output hi", code: 0, stdout: "hi\n"},
		{name: "an explicit exit", script: "exit 7", code: 7},
		{name: "a cmdlet that fails", script: "Get-Item ./definitely-not-here", code: 1},
		{name: "a native program's code", script: "pwsh -NoProfile -Command 'exit 3'", code: 3},
		{name: "a failure then a success", script: "Get-Item ./nope 2>$null; Write-Output ok", code: 0, stdout: "ok\n"},
		{name: "a native success then a failure", script: "pwsh -NoProfile -Command 'exit 0'; Get-Item ./nope", code: 1},
		{name: "a trailing comment", script: "Write-Output hi # done", code: 0, stdout: "hi\n"},
		{name: "unicode", script: "Write-Output 'naïve ✓'", code: 0, stdout: "naïve ✓\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := s.Run(t.Context(), tt.script, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.code, res.ExitCode, res.Stderr)
			if tt.stdout != "" {
				assert.Equal(t, tt.stdout, res.Stdout)
			}
		})
	}
}

func TestPwsh_CommandsDoNotSeeDetentsSecrets(t *testing.T) {
	s := pwshShell(t)
	t.Setenv("DETENT_API_KEY", "sk-detent")
	t.Setenv("DETENT_KEEP", "visible")
	res, err := s.Run(t.Context(), `Write-Output "[$env:DETENT_API_KEY][$env:DETENT_KEEP]"`, nil)
	require.NoError(t, err)
	assert.Equal(t, "[][visible]\n", res.Stdout)
}

func TestGitBash_RunsLikeSh(t *testing.T) {
	s, err := Find(GitBash)
	if err != nil {
		t.Skip("no bash here")
	}
	res, err := s.Run(t.Context(), `echo "$((1+2))"; exit 4`, nil)
	require.NoError(t, err)
	assert.Equal(t, 4, res.ExitCode)
	assert.Equal(t, "3\n", res.Stdout)
}
