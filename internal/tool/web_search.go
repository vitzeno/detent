package tool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
)

// WebSearch is one search, lowered to one curl so the sandbox stays
// the only executor and no API key ever enters the container.
type WebSearch struct{}

var _ Native = WebSearch{}

func (WebSearch) Name() string { return "web_search" }

func (WebSearch) Describe() Spec {
	return Spec{
		Description: "Search the web. Returns a numbered list of results, each with a title, " +
			"a short snippet and a URL. Read a result with bash and curl.",
		Params: []Param{
			{Name: "query", Type: TypeString, Desc: "what to search for, as you would type it", Required: true},
		},
		Mutability: event.MutRead,
		// Renders stays unset: glamour prints every link's destination,
		// and DuckDuckGo's redirects double the output.
	}
}

// Lower builds the whole URL in Go so the command stays one short
// readable line: a human approving it has to be able to read it.
func (WebSearch) Lower(a Args) (string, error) {
	target, err := searchURL(a)
	if err != nil {
		return "", err
	}
	// x-no-cache keeps a search a moment in time, not a stale answer.
	return "curl -fsS --max-time 30 -H 'x-no-cache: true' " + quote(target), nil
}

// Run makes the same request here, failing on an error status as curl -f does.
func (WebSearch) Run(ctx context.Context, a Args) capture.Result {
	target, err := searchURL(a)
	if err != nil {
		return failed(2, "web_search: %v", err)
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return failed(2, "web_search: %v", err)
	}
	req.Header.Set("x-no-cache", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return failed(1, "web_search: %v", err)
	}
	defer func() { _ = resp.Body.Close() }() // read only, so closing cannot lose anything
	if resp.StatusCode >= 400 {
		return failed(22, "web_search: the search answered %s", resp.Status)
	}
	var out bytes.Buffer
	truncated, err := capture.ScanCapped(resp.Body, false, &out, capture.MaxOutputBytes, nil)
	if err != nil {
		return capture.Result{ExitCode: 1, Stdout: out.String(), Stderr: "web_search: " + err.Error() + "\n", Truncated: truncated}
	}
	return capture.Result{Stdout: out.String(), Truncated: truncated}
}

// searchURL is the whole request, built in Go so the command stays short.
func searchURL(a Args) (string, error) {
	q := strings.TrimSpace(a.String("query"))
	if q == "" {
		return "", errors.New("query must not be empty")
	}
	// Bounds what one unapproved call can carry off the machine.
	if len(q) > maxQueryBytes {
		return "", fmt.Errorf("query is %d bytes, keep it under %d as you would type it", len(q), maxQueryBytes)
	}
	return readerPrefix + searchBase + url.QueryEscape(q), nil
}

const (
	maxQueryBytes = 256
	searchTimeout = 30 * time.Second
	// searchBase is a keyless engine, so nothing secret is in the
	// command, the log, or the container.
	searchBase = "https://lite.duckduckgo.com/lite/?q="
)

// readerPrefix is a third party returning the page as markdown, so every
// query transits Jina too. A var so a test can stand in for it.
var readerPrefix = "https://r.jina.ai/"
