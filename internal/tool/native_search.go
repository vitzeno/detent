package tool

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"unicode"
)

// walkFiles visits each regular file under dir but not symlinks, as find and
// grep -r do, in no order, going on past a directory it cannot read.
func walkFiles(ctx context.Context, dir string, skip func(name string) bool, visit func(p string), fail func(p string, err error)) error {
	d, err := os.Open(dir)
	if err != nil {
		fail(dir, err)
		return nil
	}
	defer func() { _ = d.Close() }() // read only, so closing cannot lose anything
	for {
		entries, err := d.ReadDir(256)
		for _, e := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			p := found(dir, e.Name())
			switch {
			case e.IsDir():
				if skip == nil || !skip(e.Name()) {
					if err := walkFiles(ctx, p, skip, visit, fail); err != nil {
						return err
					}
				}
			case e.Type().IsRegular():
				visit(p)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			fail(dir, err)
			return nil
		}
	}
}

// found spells a path under root as find and grep -r print it.
func found(root, name string) string {
	if strings.HasSuffix(root, "/") {
		return root + name
	}
	return root + "/" + name
}

// fnmatch is fnmatch(3) with no flags, as find and grep --include read a
// glob: * also matches a slash or a leading dot.
func fnmatch(pattern, name string) bool {
	p, s := []rune(pattern), []rune(name)
	px, sx, starP, starS := 0, 0, -1, 0
	for sx < len(s) || px < len(p) {
		if px < len(p) {
			switch c := p[px]; c {
			case '*':
				starP, starS = px, sx
				px++
				continue
			case '?':
				if sx < len(s) {
					px++
					sx++
					continue
				}
			case '[':
				if sx < len(s) {
					ok, width, valid := matchBracket(p[px:], s[sx])
					if !valid && s[sx] == '[' {
						px++
						sx++
						continue
					}
					if valid && ok {
						px += width
						sx++
						continue
					}
				}
			case '\\':
				lit := '\\'
				step := 1
				if px+1 < len(p) {
					lit, step = p[px+1], 2
				}
				if sx < len(s) && s[sx] == lit {
					px += step
					sx++
					continue
				}
			default:
				if sx < len(s) && s[sx] == c {
					px++
					sx++
					continue
				}
			}
		}
		if starP >= 0 && starS < len(s) {
			starS++
			px, sx = starP+1, starS
			continue
		}
		return false
	}
	return true
}

// matchBracket matches c against the bracket expression p opens. valid is
// false for one never closed, which fnmatch then reads as a literal [.
func matchBracket(p []rune, c rune) (ok bool, width int, valid bool) {
	i := 1
	negate := i < len(p) && (p[i] == '!' || p[i] == '^')
	if negate {
		i++
	}
	for first := true; i < len(p); first = false {
		if p[i] == ']' && !first {
			return ok != negate, i + 1, true
		}
		if p[i] == '[' && i+1 < len(p) && p[i+1] == ':' {
			if end := classEnd(p[i+2:]); end >= 0 {
				if in, known := posixClass(string(p[i+2:i+2+end]), c); known {
					ok = ok || in
					i += end + 4
					continue
				}
			}
		}
		lo := p[i]
		if lo == '\\' && i+1 < len(p) {
			i++
			lo = p[i]
		}
		i++
		hi := lo
		if i+1 < len(p) && p[i] == '-' && p[i+1] != ']' {
			hi = p[i+1]
			if hi == '\\' && i+2 < len(p) {
				hi = p[i+2]
				i++
			}
			i += 2
		}
		ok = ok || (lo <= c && c <= hi)
	}
	return false, 0, false
}

// classEnd is where the ":]" closing a class name starts in p, or -1.
func classEnd(p []rune) int {
	for i := 0; i+1 < len(p); i++ {
		if p[i] == ':' && p[i+1] == ']' {
			return i
		}
	}
	return -1
}

// posixClass reports whether c is in the named [:class:], and whether
// the name is one fnmatch knows.
func posixClass(name string, c rune) (in, known bool) {
	is, known := posixClasses[name]
	if !known {
		return false, false
	}
	return is(c), true
}

var posixClasses = map[string]func(rune) bool{
	"alpha":  unicode.IsLetter,
	"digit":  unicode.IsDigit,
	"alnum":  func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) },
	"upper":  unicode.IsUpper,
	"lower":  unicode.IsLower,
	"space":  unicode.IsSpace,
	"blank":  func(r rune) bool { return r == ' ' || r == '\t' },
	"punct":  unicode.IsPunct,
	"cntrl":  unicode.IsControl,
	"print":  unicode.IsPrint,
	"graph":  func(r rune) bool { return unicode.IsPrint(r) && r != ' ' },
	"xdigit": func(r rune) bool { return strings.ContainsRune("0123456789abcdefABCDEF", r) },
}
