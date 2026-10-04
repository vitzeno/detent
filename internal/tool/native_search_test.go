package tool

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGrep_NativeMatchesTheSandbox(t *testing.T) {
	long := map[string]string{"l.txt": "hit short\nhit " + strings.Repeat("y", outputBudget+50) + "\nhit after\n"}
	for name, c := range map[string]struct {
		args  Args
		files map[string]string
	}{
		"the working directory": {Args{"pattern": "Alpha"}, searchTree},
		"dot slash":             {Args{"pattern": "Alpha", "path": "./"}, searchTree},
		"a subdirectory":        {Args{"pattern": "Alpha", "path": "sub"}, searchTree},
		"a trailing slash":      {Args{"pattern": "Alpha", "path": "sub/"}, searchTree},
		"a dotted subdirectory": {Args{"pattern": "Alpha", "path": "./sub"}, searchTree},
		"one file":              {Args{"pattern": "Alpha", "path": "a.go"}, searchTree},
		"a .git directory":      {Args{"pattern": "Alpha", "path": ".git"}, searchTree},
		"include":               {Args{"pattern": "Alpha", "include": "*.go"}, searchTree},
		"include misses a file": {Args{"pattern": "Alpha", "path": "a.go", "include": "*.md"}, searchTree},
		"ignore case":           {Args{"pattern": "BETA", "ignore_case": true}, searchTree},
		"a regex":               {Args{"pattern": "func [a-z]+\\(\\)"}, searchTree},
		"anchors and choice":    {Args{"pattern": "^nothing$|10$"}, searchTree},
		"a class and a bound":   {Args{"pattern": "[[:digit:]]{2,}"}, searchTree},
		"a word edge":           {Args{"pattern": "\\<beta"}, searchTree},
		"no match":              {Args{"pattern": "gamma"}, searchTree},
		"a window":              {Args{"pattern": "Alpha", "max_results": 3}, searchTree},
		"a missing path":        {Args{"pattern": "Alpha", "path": "nope"}, searchTree},
		"a bad regex":           {Args{"pattern": "("}, searchTree},
		"a line over budget":    {Args{"pattern": "hit"}, long},
		"a line over a read":    {Args{"pattern": "hit$|^z"}, map[string]string{"l.txt": "hit\n" + strings.Repeat("z", 3*readChunk) + "hit\nhit\n"}},
		"many matches":          {Args{"pattern": "a", "max_results": 900}, map[string]string{"m/1.txt": numbered(700), "m/2.txt": numbered(700), "m.txt": numbered(50)}},
	} {
		t.Run(name, func(t *testing.T) { cParity(t, Grep{}, c.args, c.files) })
	}
}

func TestFindFiles_NativeMatchesTheSandbox(t *testing.T) {
	names := map[string]string{}
	for name := range searchTree {
		names[name] = ""
	}
	names["wt/.git"] = "gitdir: elsewhere\n" // how a worktree spells it
	names["wt/a.go"] = ""
	names["-x/a.go"] = ""
	names["a=b/c.go"] = ""
	for name, c := range map[string]struct {
		args  Args
		files map[string]string
	}{
		"by name":                {Args{"pattern": "*.go"}, names},
		"dot slash":              {Args{"pattern": "*.go", "path": "./"}, names},
		"a subdirectory":         {Args{"pattern": "*.go", "path": "sub"}, names},
		"a trailing slash":       {Args{"pattern": "*", "path": "sub/"}, names},
		"a dotted subdirectory":  {Args{"pattern": "*", "path": "./sub"}, names},
		"a path that is a flag":  {Args{"pattern": "*.go", "path": "-x"}, names},
		"a path awk misreads":    {Args{"pattern": "*.go", "path": "a=b"}, names},
		"one file":               {Args{"pattern": "a.*", "path": "a.go"}, names},
		"a .git directory":       {Args{"pattern": "*", "path": ".git"}, names},
		"everything":             {Args{"pattern": "*"}, names},
		"by path":                {Args{"pattern": "*/sub/*.go"}, names},
		"by path from a subdir":  {Args{"pattern": "sub/*"}, names},
		"by path with no prefix": {Args{"pattern": "sub/*", "path": "sub"}, names},
		"a negated class":        {Args{"pattern": "[!a_]*.go"}, names},
		"an escaped bracket":     {Args{"pattern": "\\[x].go"}, names},
		"a named class":          {Args{"pattern": "[[:upper:]].go"}, names},
		"a question mark":        {Args{"pattern": "?.go"}, names},
		"no match":               {Args{"pattern": "*.rs"}, names},
		"a window":               {Args{"pattern": "*", "max_results": 2}, names},
		"a missing path":         {Args{"pattern": "*", "path": "nope"}, names},
	} {
		t.Run(name, func(t *testing.T) { cParity(t, FindFiles{}, c.args, c.files) })
	}
}

func TestSkill_NativeMatchesTheSandbox(t *testing.T) {
	many := map[string]string{"s/SKILL.md": "# Many\n"}
	for i := range 60 {
		many[fmt.Sprintf("s/ref/%02d.md", i)] = ""
	}
	for name, c := range map[string]struct {
		dir   string
		files map[string]string
	}{
		"a body and its files": {"s", map[string]string{
			"s/SKILL.md": "# Release\nTag it.\n", "s/scripts/tag.sh": "git tag", "s/ref/notes.md": "n", "s/nested/SKILL.md": "x",
		}},
		"only a body":        {"s", map[string]string{"s/SKILL.md": "# Alone\n"}},
		"an empty body":      {"s", map[string]string{"s/SKILL.md": ""}},
		"a long body":        {"s", map[string]string{"s/SKILL.md": numbered(800), "s/x.md": ""}},
		"too many files":     {"s", many},
		"a dir like a flag":  {"-s", map[string]string{"-s/SKILL.md": "# Flag\n", "-s/x.md": ""}},
		"no SKILL.md at all": {"s", map[string]string{"s/x.md": ""}},
	} {
		t.Run(name, func(t *testing.T) {
			s := NewSkill([]SkillEntry{{Name: "x", Dir: c.dir}})
			cParity(t, s, Args{"name": "x"}, c.files)
		})
	}
}

// grep exits 2 for an error, which keeps what it did find.
func TestGrep_NativeSaysWhatItCouldNotRead(t *testing.T) {
	got := Grep{}.Run(t.Context(), Args{"pattern": "x", "path": filepath.Join(t.TempDir(), "nope")})
	assert.Equal(t, 2, got.ExitCode)
	assert.Contains(t, got.Stderr, "nope: no such file or directory")

	got = Grep{}.Run(t.Context(), Args{"pattern": "("})
	assert.Equal(t, 2, got.ExitCode)
	assert.Contains(t, got.Stderr, "missing closing )")
}

// A NUL anywhere makes a file binary, not only in its first block as grep -I sees it.
func TestGrep_NativeSkipsALateBinaryFile(t *testing.T) {
	t.Chdir(tree(t, map[string]string{"late.bin": strings.Repeat("Alpha\n", 20000) + "\x00"}))
	assert.Equal(t, "[no matches]\n", Grep{}.Run(t.Context(), Args{"pattern": "Alpha"}).Stdout)
}

func TestFnmatch_ReadsGlobsAsFindDoes(t *testing.T) {
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"*.go", "a.go", true},
		{"*.go", ".a.go", true},
		{"*/x", "a/b/x", true},
		{"a?c", "abc", true},
		{"a?c", "a/c", true},
		{"[a-c]x", "bx", true},
		{"[!a-c]x", "bx", false},
		{"[^a-c]x", "dx", true},
		{"[]]", "]", true},
		{"[!]]", "a", true},
		{"[[:digit:]]*", "9lives", true},
		{"[[:bogus:]]", "b", false},
		{"\\*", "*", true},
		{"\\*", "a", false},
		{"[x", "[x", true},
		{"[x", "x", false},
		{"é?", "éa", true},
		{"*a*b*", "xxaxxbxx", true},
		{"*a*b", "xxaxxbxxc", false},
		{"", "", true},
		{"", "a", false},
	} {
		assert.Equal(t, c.want, fnmatch(c.pattern, c.name), "%q against %q", c.pattern, c.name)
	}
}

// cParity is parity in the C locale, which is what a sandbox's sort
// collates in and what the native side's byte order matches.
func cParity(t *testing.T, tl Native, args Args, files map[string]string) {
	t.Helper()
	t.Setenv("LC_ALL", "C")
	parity(t, tl, args, files)
}

var searchTree = map[string]string{
	"a.go":          "package a\nfunc Alpha() {}\nfunc beta() {}\n",
	"a-b.go":        "alphabeta\n",
	"B.go":          "Alpha at the top\n",
	"_x.txt":        "nothing\nAlpha\n",
	"sub/b.go":      "x\nx\nx\nx\nx\nx\nx\nx\nAlpha 9\nAlpha 10\n",
	"sub/deep/c.md": "Alpha in prose",
	".hid/d.go":     "Alpha hidden\n",
	".git/config":   "Alpha\n",
	"sub/.git/e":    "Alpha\n",
	"bin.dat":       "Alpha\x00\x01\n",
	"[x].go":        "Alpha\n",
}
