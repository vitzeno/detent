package engine

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// growthSamples is how many recent Steps the growth rate averages over.
const growthSamples = 6

// PromptSizer names the system prompt's pieces. model.Client satisfies it,
// and without one the prompt is left out of the fixed parts.
type PromptSizer interface {
	PromptParts() []model.PromptPart
}

// gauge is the bytes-to-tokens ratio the last Step showed, and the last
// exact measurement, which nothing since has changed.
type gauge struct {
	mu      sync.Mutex
	perByte float64
	samples []int
	last    event.ContextMeasured
	lastKey sizeKey
}

// sizeKey changes whenever the request would: a message added, or a tool.
type sizeKey struct{ mark, fixed int }

// part is a slice of the request before it is turned into tokens.
type part struct {
	event.ContextPart
	bytes, largest int
}

// measure sizes the request the model was just sent, or would be sent
// next. sent is the endpoint's own count for it, or 0 when none was sent.
func (e *Engine) measure(sent int) event.ContextMeasured {
	var msgs []event.Message
	var starts []start
	var dropped int
	e.root.lock(func() {
		msgs, starts, dropped = slices.Clone(e.root.tr.msgs), slices.Clone(e.root.tr.starts), e.root.tr.dropped
	})
	fixed := e.fixedParts()
	history := historyParts(msgs, starts, dropped, e.current() != nil)
	key := sizeKey{len(msgs) + dropped, bytesOf(fixed)}

	g := &e.gauge
	g.mu.Lock()
	defer g.mu.Unlock()
	// Nothing changed since the Step that was counted exactly, so say that again.
	if sent == 0 && g.last.Exact && key == g.lastKey {
		return g.last
	}
	est := bytesOf(fixed) + bytesOf(history)
	if sent > 0 && est > 0 {
		g.perByte = float64(sent) / float64(est)
	}
	perByte := g.perByte
	if perByte == 0 {
		perByte = 1.0 / bytesPerToken
	}
	out := event.ContextMeasured{
		Budget: e.budget(), Total: sent, Exact: sent > 0,
		Fixed: tokens(fixed, perByte), History: tokens(history, perByte),
	}
	if !out.Exact {
		out.Total = int(float64(est) * perByte)
	}
	if sent > 0 {
		g.sample(sum(out.History))
		g.last, g.lastKey = out, key
	}
	out.Growth = g.growth()
	if sent > 0 {
		g.last.Growth = out.Growth
	}
	return out
}

// fixedParts is what every Step resends: detent's prompt, the
// instructions, and the tool schemas, grouped by who offers them.
func (e *Engine) fixedParts() []part {
	var out []part
	if s, ok := e.root.model.(PromptSizer); ok {
		for _, p := range s.PromptParts() {
			out = append(out, part{event.ContextPart{Name: p.Name, Detail: p.Detail}, p.Bytes, 0})
		}
	}
	// Each tool says which row it belongs to, so a new kind needs nothing here.
	type group struct {
		bytes, n int
		detail   string
	}
	groups := map[string]*group{}
	for _, schema := range e.root.tools.Schemas() {
		raw, _ := json.Marshal(schema)
		fn, _ := schema["function"].(map[string]any)
		name, _ := fn["name"].(string)
		var spec tool.Spec
		if t, ok := e.root.tools.Lookup(name); ok {
			spec = t.Describe()
		}
		key := cmp.Or(spec.Group, "tools")
		g := groups[key]
		if g == nil {
			g = &group{detail: spec.GroupDetail}
			groups[key] = g
		}
		g.bytes += len(raw)
		g.n++
	}
	for key, g := range groups {
		detail := g.detail
		switch {
		case detail != "":
		case key == "tools":
			detail = fmt.Sprintf("%d built-in", g.n)
		case g.n == 1:
			detail = "1 tool"
		default:
			detail = fmt.Sprintf("%d tools", g.n)
		}
		out = append(out, part{event.ContextPart{Name: key, Detail: detail}, g.bytes, 0})
	}
	slices.SortFunc(out, func(a, b part) int { return cmp.Or(b.bytes-a.bytes, strings.Compare(a.Name, b.Name)) })
	return out
}

// historyParts splits the transcript by request, oldest first, the order
// compaction folds it in. A compaction note at the front is its own part.
func historyParts(msgs []event.Message, starts []start, dropped int, open bool) []part {
	var out []part
	first := len(msgs)
	// Compaction leaves its note at 0, so a start there or before was folded into it.
	noted := len(msgs) > 0 && isCompactionNote(msgs[0])
	isFolded := func(s start) bool { i := s.at - dropped; return i < 0 || (i == 0 && noted) }
	var folded, kept []start
	for _, s := range starts {
		if isFolded(s) {
			folded = append(folded, s)
		} else {
			kept = append(kept, s)
			first = min(first, s.at-dropped)
		}
	}
	head := msgs[:first]
	if len(head) > 0 && noted {
		summary := part{ContextPart: event.ContextPart{Name: "summary", Detail: requests(folded)}, bytes: msgBytes(head[:1])}
		out, head = append(out, summary), head[1:]
	}
	if len(head) > 0 {
		// What is left of a request compaction cut into, or notes from before any request.
		p := part{ContextPart: event.ContextPart{Name: "notes"}}
		if len(folded) > 0 {
			last := folded[len(folded)-1]
			p.Name, p.N, p.Detail = last.prompt, last.n, "partly summarised"
		}
		out = append(out, withLargest(p, head))
	}
	for i, s := range kept {
		end := len(msgs)
		if i+1 < len(kept) {
			end = kept[i+1].at - dropped
		}
		p := part{ContextPart: event.ContextPart{Name: s.prompt, N: s.n, Open: open && i == len(kept)-1}}
		out = append(out, withLargest(p, msgs[s.at-dropped:end]))
	}
	return out
}

// withLargest sizes p from msgs and names its biggest message: the
// command whose output it was, or the model's reply.
func withLargest(p part, msgs []event.Message) part {
	p.bytes = msgBytes(msgs)
	calls := map[string]string{}
	for _, m := range msgs {
		for _, c := range m.Requests {
			calls[c.ID] = event.Command(c.Name, c.Args)
		}
	}
	for i, m := range msgs {
		if i == 0 || len(m.Content) <= p.largest {
			continue
		}
		switch m.Role {
		case event.RoleTool:
			p.Largest, p.largest = calls[m.RequestID], len(m.Content)
		case event.RoleAssistant:
			p.Largest, p.largest = "a reply", len(m.Content)
		case event.RoleSystem, event.RoleUser:
			// Only what a command printed or the model said is named.
		}
	}
	return p
}

func isCompactionNote(m event.Message) bool {
	return m.Role == event.RoleUser &&
		(strings.HasPrefix(m.Content, summaryMarker) || strings.HasSuffix(m.Content, droppedMarker))
}

// requests names the folded ones: "request 2", "requests 1 to 3".
func requests(folded []start) string {
	switch len(folded) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("request %d", folded[0].n)
	}
	return fmt.Sprintf("requests %d to %d", folded[0].n, folded[len(folded)-1].n)
}

// sample records history after a Step, starting over when it shrank.
func (g *gauge) sample(history int) {
	if n := len(g.samples); n > 0 && history < g.samples[n-1] {
		g.samples = g.samples[:0]
	}
	g.samples = append(g.samples, history)
	if len(g.samples) > growthSamples {
		g.samples = g.samples[1:]
	}
}

// growth is the average history a recent Step added.
func (g *gauge) growth() int {
	n := len(g.samples)
	if n < 2 {
		return 0
	}
	return (g.samples[n-1] - g.samples[0]) / (n - 1)
}

func tokens(parts []part, perByte float64) []event.ContextPart {
	out := make([]event.ContextPart, len(parts))
	for i, p := range parts {
		out[i] = p.ContextPart
		out[i].Tokens = int(float64(p.bytes) * perByte)
		out[i].LargestTokens = int(float64(p.largest) * perByte)
	}
	return out
}

func bytesOf(parts []part) int {
	n := 0
	for _, p := range parts {
		n += p.bytes
	}
	return n
}

func sum(parts []event.ContextPart) int {
	n := 0
	for _, p := range parts {
		n += p.Tokens
	}
	return n
}
