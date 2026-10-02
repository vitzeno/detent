package tool

import (
	"errors"
	"fmt"

	"github.com/vitzeno/detent/event"
)

// EditFile replaces one exact piece of a file, so a small change does
// not mean resending the whole file through write_file.
type EditFile struct{}

func (EditFile) Name() string { return "edit_file" }

func (EditFile) Describe() Spec {
	return Spec{
		Description: "Replace an exact piece of text in an existing file. old_string must match the file " +
			"exactly, whitespace included, and appear once unless replace_all is set. " +
			"Include enough surrounding lines to make it unique. Read the file first.",
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

func (EditFile) Lower(a Args) (string, error) {
	path, old, repl := a.String("path"), a.String("old_string"), a.String("new_string")
	switch {
	case path == "":
		return "", errors.New("path must not be empty")
	case old == "":
		return "", errors.New("old_string must not be empty, use write_file to create or overwrite a file")
	case old == repl:
		return "", errors.New("old_string and new_string are the same, so there is nothing to change")
	}
	all := "0"
	if a.Bool("replace_all", false) {
		all = "1"
	}
	oldDelim, newDelim := delimFor(old, "DETENT_OLD"), delimFor(repl, "DETENT_NEW")
	// A copy first, so the result can show the change as a diff.
	// Each heredoc gains a newline, which the script chops, so the text arrives byte for byte.
	return fmt.Sprintf("before=$(mktemp) && cp -- %s \"$before\" 2>/dev/null\nperl -e %s -- %s %s 3<<'%s' 4<<'%s'; rc=$?; %s\n%s\n%s\n%s\n%s",
		quote(path), quote(editScript), quote(path), all, oldDelim, newDelim, showDiff("$before", path),
		old, oldDelim, repl, newDelim), nil
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

// delimFor grows base until no line of body could end its heredoc early.
func delimFor(body, base string) string {
	delim := base
	for n := 0; containsLine(body, delim); n++ {
		delim = fmt.Sprintf("%s_%d", base, n)
	}
	return delim
}
