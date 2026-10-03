package tool

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/vitzeno/detent/internal/capture"
)

// Native is a Tool that also runs in this process, which is how it runs on the
// host on every OS. The sandbox still runs Lower, and both must print the same.
type Native interface {
	Tool
	Run(ctx context.Context, args Args) capture.Result
}

// failed is a native tool refusing or failing, said the way a command would.
func failed(code int, format string, a ...any) capture.Result {
	return capture.Result{ExitCode: code, Stderr: fmt.Sprintf(format, a...) + "\n"}
}

// windowLines is window's awk script in Go: limit lines from start, stopping
// early at outputBudget bytes, with more or empty as the footer.
func windowLines(r io.Reader, start, limit int, more, empty string) (string, error) {
	var b strings.Builder
	sc := bufio.NewReader(r)
	n, used, stop := 0, 0, 0
	for {
		line, err := sc.ReadString('\n')
		if line == "" && err != nil {
			if err != io.EOF {
				return b.String(), err
			}
			break
		}
		n++
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case n < start || stop > 0:
			continue
		case n >= start+limit:
			stop = n
			continue
		case used > 0 && used+len(line)+1 > outputBudget:
			stop = n
			continue
		}
		if len(line) > outputBudget {
			line = cutRunes(line, outputBudget) + " [line cut]"
		}
		b.WriteString(line)
		b.WriteByte('\n')
		used += len(line) + 1
	}
	switch {
	case stop > 0:
		b.WriteString(footer(more, n-stop+1, stop))
	case n < start:
		b.WriteString(footer(empty, n))
	}
	return b.String(), nil
}

// footer fills as many of args as format has verbs, as awk's printf does,
// since some footers say where to resume and some do not.
func footer(format string, args ...int) string {
	n := strings.Count(format, "%d")
	vals := make([]any, 0, n)
	for _, a := range args[:min(n, len(args))] {
		vals = append(vals, a)
	}
	return fmt.Sprintf(format, vals...) + "\n"
}

// cutRunes shortens s to at most n bytes without splitting a rune.
func cutRunes(s string, n int) string {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
