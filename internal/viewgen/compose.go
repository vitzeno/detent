package viewgen

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/usage"
	"github.com/vitzeno/detent/logging"
	"github.com/vitzeno/detent/viewspec"
)

// Compose builds a spec by asking the judge closed questions and
// assembling the answers, rather than asking a model to write one.
//
// It replaced generation on two counts. Generation ran 31s median
// against 300ms here, and a generated spec could name a widget, a role
// or a field that did not exist, which all happened. Neither is
// possible from a list the program built.
func (g *Generator) Compose(ctx context.Context, req Request) (Result, error) {
	log := logging.For(logging.Viewgen)
	key := Key(req.Command, req.Kind)
	if spec, ok := g.Store.Load(key); ok && g.usable(ctx, req, spec, SourceSaved) {
		return Result{Spec: spec, Key: key, Source: SourceSaved}, nil
	}
	if !g.worthAsking(req) {
		log.InfoContext(ctx, "not asking for a view", logging.KeyEvent, logging.ViewSkipped,
			"kind", req.Kind, "lines", lines(req.Output), logging.KeyReason, g.skipReason(req))
		if spec, ok := g.shipped(ctx, req, key); ok {
			return spec, nil
		}
		return Result{}, ErrNotWorth
	}
	if g.Judge == nil {
		return Result{}, ErrNoJudge
	}

	c := &composer{g: g, req: req, log: log}
	spec, err := c.run(ctx)
	if err != nil {
		log.InfoContext(ctx, "could not compose a view", logging.KeyEvent, logging.ViewDeclined,
			"questions", c.asked, logging.KeyReason, err.Error())
		if got, ok := g.shipped(ctx, req, key); ok {
			got.Usage = c.used
			return got, nil
		}
		return Result{Usage: c.used}, ErrNoneFit
	}

	spec.Version, spec.Match = viewspec.Version, Normalise(req.Command)
	if err := g.Store.Save(key, spec); err != nil {
		return Result{Usage: c.used}, err
	}
	log.InfoContext(ctx, "drawing a composed view", logging.KeyEvent, logging.ViewAccepted,
		"key", key, "questions", c.asked, "parse", spec.Parse.Kind, "blocks", len(spec.Blocks))
	return Result{Spec: spec, Key: key, Source: SourceGenerated, Usage: c.used}, nil
}

// shipped is the floor: detent's own spec for this command, when it
// can draw this particular output.
func (g *Generator) shipped(ctx context.Context, req Request, key string) (Result, bool) {
	spec, ok := seed(req.Command)
	if !ok || !g.usable(ctx, req, spec, SourceShipped) {
		return Result{}, false
	}
	return Result{Spec: spec, Key: key, Source: SourceShipped}, true
}

// composer carries one composition: the request, what it has asked,
// and what that cost.
type composer struct {
	g     *Generator
	req   Request
	log   logger
	asked int
	used  usage.Usage
}

// run is the pipeline, and the order is the point. The header decides
// how the parse reads, the parse decides which fields exist, and only
// then is there anything to choose a widget for.
func (c *composer) run(ctx context.Context) (*viewspec.Spec, error) {
	parse, err := c.parse(ctx)
	if err != nil {
		return nil, err
	}
	fields, rows, err := readWith(parse, c.req.Output)
	if err != nil {
		return nil, fmt.Errorf("parse %q read nothing: %w", parse.Kind, err)
	}

	blocks, err := c.blocks(ctx, parse, fields, rows)
	if err != nil {
		return nil, err
	}
	spec := &viewspec.Spec{Version: viewspec.Version, Parse: parse, Blocks: blocks}
	if err := draws(spec, c.g.registry(), c.req.Output); err != nil {
		return nil, fmt.Errorf("composed view does not draw: %w", err)
	}
	return spec, nil
}

// parse settles where the header is, then which kind reads it. That
// order, because columns and fixed differ only in whether the header's
// own names contain spaces.
func (c *composer) parse(ctx context.Context) (viewspec.Parse, error) {
	skip := c.header(ctx)
	kind, err := c.parseKind(ctx, headerLine(c.req.Output, skip))
	if err != nil {
		return viewspec.Parse{}, err
	}
	if kind == parseNone {
		return viewspec.Parse{}, errNothingToDraw
	}
	p := viewspec.Parse{Kind: kind}
	switch kind {
	case "columns", "fixed", "delimited":
		p.Header = true
	case "pairs":
		p.Sep = "="
	case "lines":
		// The one pattern composition writes, and it is fixed: one
		// field holding the whole line. A pattern naming three parts
		// is the one thing a choice cannot express, so output needing
		// that draws plainly rather than wrongly.
		p.Pattern = wholeLine
	}
	if !p.Header {
		return p, nil
	}
	return honour(p, skip, c.req.Output), nil
}

// header asks which line names the columns. A count is arithmetic; the
// lines themselves are a choice. -1 means there is no header at all,
// which is ls -la and every other bare listing.
func (c *composer) header(ctx context.Context) int {
	lines := strings.Split(strings.TrimRight(c.req.Output, "\n"), "\n")
	if len(lines) < 2 {
		return 0
	}
	criteria := map[string]any{
		"none": "None of these is a header. Every line is data, as in ls -la.",
	}
	for i := range min(len(lines), headerCandidates) {
		criteria[strconv.Itoa(i)] = fmt.Sprintf("line %d: %s", i+1, head(lines[i], 120))
	}
	answers, ok := c.ask(ctx, classify.Questions{
		"header_line": {
			Instructions: "Which of these lines names the columns of the table below it? " +
				"Choose the first line if the output starts with its header, and a later one " +
				"when a total, a title or a blank line comes first.",
			Choice: &classify.ChoiceQuestion{Criteria: criteria},
		},
	})
	if !ok {
		return 0
	}
	if answers["header_line"].Choice == "none" {
		return -1
	}
	n, err := strconv.Atoi(answers["header_line"].Choice)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// parseKind chooses how to read the bytes. header is what the previous
// question found, so the choice is made looking at the real header
// rather than at whatever happens to be on the first line.
func (c *composer) parseKind(ctx context.Context, header string) (string, error) {
	state := c.state()
	if header != "" {
		state["header_line"] = header
	}
	answers, ok := c.askState(ctx, state, classify.Questions{
		"parse_kind": {
			Instructions: "How should this command's output be read into rows? Choose the " +
				"kind that describes the shape of the bytes, not what they mean. Where " +
				"header_line is given, it is the line naming the columns: read it to decide " +
				"whether the headings are single words or contain spaces.",
			Choice: &classify.ChoiceQuestion{Criteria: parseCriteria},
		},
	})
	if !ok {
		return "", errJudgeSilent
	}
	return answers["parse_kind"].Choice, nil
}

// blocks chooses what draws the rows and what field each one reads.
// The widgets are whatever the render kind allows and the fields are
// what the parse produced, so neither can name something absent.
func (c *composer) blocks(ctx context.Context, parse viewspec.Parse,
	fields []string, rows []viewspec.Row) ([]viewspec.Block, error) {
	guide := describe(c.g.registry())
	bodies, summaries := split(guide, prune(c.g.registry(), c.req.Kind).Kinds())
	if len(bodies) == 0 {
		return nil, fmt.Errorf("no body widget is offered for %s", c.req.Kind)
	}

	state := c.state()
	state["parse_kind"], state["fields_found"] = parse.Kind, fields
	answers, ok := c.askState(ctx, state, classify.Questions{
		"body": {
			Instructions: "Which widget should draw the body of this output? " +
				"Choose what a human would read this best as.",
			Choice: &classify.ChoiceQuestion{Criteria: criteriaFor(guide, bodies)},
		},
		"summary": {
			Instructions: "Which widget, if any, should sit above the body as a one-line " +
				"summary? Choose none unless it genuinely adds something.",
			Choice: &classify.ChoiceQuestion{Criteria: withNone(criteriaFor(guide, summaries))},
		},
	})
	if !ok {
		return nil, errJudgeSilent
	}
	chosen, summary := answers["body"].Choice, answers["summary"].Choice

	// One call for every field every chosen widget needs.
	qs := classify.Questions{}
	for _, s := range guide[chosen].Needs {
		qs[s.Name] = fieldQuestion(s, fields, rows)
	}
	if summary != choiceNone {
		for _, s := range guide[summary].Needs {
			qs["summary_"+s.Name] = fieldQuestion(s, fields, rows)
		}
	}
	picked := classify.Answers{}
	if len(qs) > 0 {
		got, ok := c.askState(ctx, state, qs)
		if !ok {
			return nil, errJudgeSilent
		}
		picked = got
	}

	var out []viewspec.Block
	if summary != choiceNone {
		out = append(out, block(summary, guide[summary].Needs, picked, "summary_"))
	}
	return append(out, block(chosen, guide[chosen].Needs, picked, "")), nil
}

// ask and askState put one batch of questions and record the cost.
func (c *composer) ask(ctx context.Context, qs classify.Questions) (classify.Answers, bool) {
	return c.askState(ctx, c.state(), qs)
}

func (c *composer) askState(ctx context.Context, state map[string]any,
	qs classify.Questions) (classify.Answers, bool) {
	answers, u, ok := classify.AskOrFallback(ctx, c.g.Judge, classify.State(state), qs)
	c.asked++
	if !ok {
		c.log.WarnContext(ctx, "the judge did not answer", logging.KeyEvent, logging.LLMError,
			"question", len(qs))
		return nil, false
	}
	c.used = add(c.used, usage.Usage{
		PromptTokens: u.InputTokens, CompletionTokens: u.OutputTokens, Model: u.Model})
	return answers, true
}

// state is what every question is answered against. Bounded: a judge
// deciding shape needs a sample, not the whole output.
func (c *composer) state() map[string]any {
	return map[string]any{
		"command": c.req.Command,
		"output":  head(c.req.Output, MaxJudgeBytes),
	}
}
