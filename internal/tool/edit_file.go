package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
)

// EditFile replaces one exact piece of a file, so a small change does
// not mean resending the whole file through write_file.
type EditFile struct{}

var _ Native = EditFile{}

func (EditFile) Name() event.ToolName { return event.ToolEditFile }

func (EditFile) Describe() Spec {
	return Spec{
		Description: "Replace an exact piece of text in an existing file. old_string must match the file " +
			"exactly, whitespace included, and appear once unless replace_all is set. " +
			"Include enough surrounding lines to make it unique. Read the file first. " +
			"To add lines, put the line they go next to in old_string, and that line plus the new ones " +
			"in new_string. Appending to the end of a file works the same way, from its last line.",
		Params: []Param{
			{Name: "path", Type: TypeString, Desc: "path to the file", Required: true},
			{Name: "old_string", Type: TypeString, Desc: "the exact text to replace", Required: true},
			{Name: "new_string", Type: TypeString, Desc: "the text to put in its place", Required: true},
			{Name: "replace_all", Type: TypeBool, Desc: "replace every occurrence (default false)"},
		},
		Mutability: event.MutWorkspace,
		Renders:    event.RendersDiff,
	}
}

// Lower edits with perl and shows the change with diff and awk.
func (EditFile) Lower(a Args) (string, error) {
	path, old, repl, err := editArgs(a)
	if err != nil {
		return "", err
	}
	all := "0"
	if a.Bool("replace_all", false) {
		all = "1"
	}
	oldDelim, newDelim := delimFor(old, "DETENT_OLD"), delimFor(repl, "DETENT_NEW")
	// A copy first, so the result can show the change as a diff.
	// Each heredoc gains a newline, which the script chops, so the text arrives byte for byte.
	return fmt.Sprintf("before=$(mktemp) && cp -- %s \"$before\" 2>/dev/null\ncommand -v perl >/dev/null || echo 'edit_file needs perl, which is not installed here: use write_file' >&2; perl -e %s -- %s %s 3<<'%s' 4<<'%s'; rc=$?; %s\n%s\n%s\n%s\n%s",
		quote(path), quote(editScript), quote(path), all, oldDelim, newDelim, showDiff("$before", path),
		old, oldDelim, repl, newDelim), nil
}

// Run is editScript in Go, with the same messages and exit statuses.
func (EditFile) Run(ctx context.Context, a Args) capture.Result {
	p, old, repl, err := editArgs(a)
	if err != nil {
		return failed(2, "edit_file: %v", err)
	}
	b, err := readCapped(ctx, p, maxEditBytes)
	if ctx.Err() != nil {
		return stopped(event.ToolEditFile, "", ctx.Err())
	}
	if err != nil {
		return failed(perlStatus(err), "edit_file: %v", err)
	}
	before := string(b)
	n := strings.Count(before, old)
	if n == 0 {
		old, repl, n = asCRLF(before, old, repl)
	}
	switch {
	case n == 0:
		return failed(255, "edit_file: old_string is not in %s", p)
	case n > 1 && !a.Bool("replace_all", false):
		return failed(255, "edit_file: old_string is in %s %d times, so give more context or set replace_all", p, n)
	}
	if grown := int64(len(before)) + int64(n)*int64(len(repl)-len(old)); grown > maxEditBytes {
		return failed(255, "edit_file: %s would grow to %d MB, more than edit_file writes (%d MB), so change it with a command", p, grown>>20, maxEditBytes>>20)
	}
	if err := ctx.Err(); err != nil {
		return stopped(event.ToolEditFile, "", err)
	}
	after := strings.Replace(before, old, repl, n)
	if err := writeAsShell(p, after); err != nil {
		return failed(perlStatus(err), "edit_file: %v", err)
	}
	return capture.Result{Stdout: fmt.Sprintf("replaced %d in %s\n", n, p) + changeShown(p, before, after)}
}

func editArgs(a Args) (p, old, repl string, err error) {
	p, old, repl = a.String("path"), a.String("old_string"), a.String("new_string")
	switch {
	case p == "":
		return "", "", "", errors.New("path must not be empty")
	case old == "":
		return "", "", "", errors.New("old_string must not be empty, use write_file to create or overwrite a file")
	case old == repl:
		return "", "", "", errors.New("old_string and new_string are the same, so there is nothing to change")
	}
	return p, old, repl, nil
}

// editScript does the replacement in perl, which every Debian and Ubuntu
// image carries, matching literally with index rather than a regex.
const editScript = `use strict; local $/;
my ($p, $all) = @ARGV;
open(my $fo, "<&=", 3) or die "edit_file: $!\n"; binmode $fo; my $o = <$fo>; chop $o;
open(my $fn, "<&=", 4) or die "edit_file: $!\n"; binmode $fn; my $n = <$fn>; chop $n;
open(my $f, "<", $p) or die "edit_file: $p: $!\n"; binmode $f; my $s = <$f>; close $f;
$s = "" unless defined $s;
my @at; my $i = 0;
while (($i = index($s, $o, $i)) >= 0) { push @at, $i; $i += length $o }
die "edit_file: old_string is not in $p\n" unless @at;
die "edit_file: old_string is in $p " . @at . " times, so give more context or set replace_all\n" if @at > 1 && !$all;
@at = ($at[0]) unless $all;
substr($s, $_, length $o) = $n for reverse @at;
open($f, ">", $p) or die "edit_file: $p: $!\n"; binmode $f; print $f $s; close $f or die "edit_file: $p: $!\n";
print "replaced " . @at . " in $p\n";`

// perlStatus is the status perl's die exits with: errno when one is set.
func perlStatus(err error) int {
	var errno syscall.Errno
	if errors.As(err, &errno) && errno > 0 && errno < 256 {
		return int(errno)
	}
	return 255
}

// asCRLF retries a missed match in a CRLF file with the model's \n as \r\n, since
// read_file shows such a file without its \r. The new text gets CRLF to match.
func asCRLF(before, old, repl string) (string, string, int) {
	if !strings.Contains(before, "\r\n") || !strings.Contains(old, "\n") || strings.Contains(old, "\r\n") {
		return old, repl, 0
	}
	crlf := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n") }
	if n := strings.Count(before, crlf(old)); n > 0 {
		return crlf(old), crlf(repl), n
	}
	return old, repl, 0
}
