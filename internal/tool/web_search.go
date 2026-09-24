package tool

import (
	"errors"
	"net/url"
	"strings"

	"github.com/vitzeno/detent/event"
)

// WebSearch is one search, lowered to one curl so the sandbox stays
// the only executor and no API key ever enters the container.
type WebSearch struct{}

func (WebSearch) Name() string { return "web_search" }

func (WebSearch) Describe() Spec {
	return Spec{
		Description: "Search the web. Returns a numbered list of results, each with a title, " +
			"a short snippet and a URL. Read a result with bash and curl.",
		Params: []Param{
			{Name: "query", Type: TypeString, Desc: "what to search for, as you would type it", Required: true},
		},
		Mutability: event.MutRead,
	}
}

// Lower builds the whole URL in Go so the command stays one short
// readable line: a human approving it has to be able to read it.
func (WebSearch) Lower(a Args) (string, error) {
	q := strings.TrimSpace(a.String("query"))
	if q == "" {
		return "", errors.New("query must not be empty")
	}
	target := searchBase + url.QueryEscape(q)
	// x-no-cache keeps a search a moment in time, not a stale answer.
	return "curl -sS --max-time 30 -H 'x-no-cache: true' " + quote(readerPrefix+target), nil
}

const (
	// searchBase is a keyless engine, so nothing secret is in the
	// command, the log, or the container.
	searchBase = "https://lite.duckduckgo.com/lite/?q="
	// readerPrefix is a third party: it returns that page as markdown, so
	// every web_search query transits Jina as well as the engine.
	readerPrefix = "https://r.jina.ai/"
)
