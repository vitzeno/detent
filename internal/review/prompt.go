package review

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// prompt is what a submitted review says to the agent: the human's comments as
// instructions, a reviewer's as opinions, so a model's words never pass as the human's.
func prompt(v event.SubmitReview) string {
	var human, agent []event.ReviewComment
	for _, c := range v.Comments {
		if c.ReplyTo != uuid.Nil && slices.ContainsFunc(v.Comments, func(p event.ReviewComment) bool {
			return p.ID == c.ReplyTo
		}) {
			continue
		}
		if c.Author == "" {
			human = append(human, c)
		} else {
			agent = append(agent, c)
		}
	}
	var b strings.Builder
	b.WriteString(opening(v) + "\n")
	section(&b, "From the human. Act on these:", human, v.Comments)
	section(&b, "From a reviewer agent, kept by the human. Weigh each, and say where you disagree:",
		agent, v.Comments)
	return strings.TrimSuffix(b.String(), "\n")
}

// opening says whose changes these are: the agent's, or the human's own, which
// it must not take for its work to defend or undo.
func opening(v event.SubmitReview) string {
	switch v.Scope {
	case event.ScopeSession:
		return "Review of everything you changed this session, up to request " + strconv.Itoa(v.Request) + "."
	case event.ScopeSince:
		return "Review of changes the human made since your request " + strconv.Itoa(v.Request) +
			" ended. You did not write these."
	case event.ScopeBranch:
		return "Review of this branch against " + v.Against +
			", committed and not, which holds the human's work as well as yours."
	case event.ScopeRequest:
	}
	return "Review of your changes in request " + strconv.Itoa(v.Request) + "."
}

// section writes each comment under heading, with what it quotes and its replies.
func section(b *strings.Builder, heading string, comments, all []event.ReviewComment) {
	if len(comments) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s\n", heading)
	for _, c := range comments {
		fmt.Fprintf(b, "\n%s\n", where(c))
		for l := range strings.SplitSeq(c.Quote, "\n") {
			fmt.Fprintf(b, "    %s\n", l)
		}
		fmt.Fprintf(b, "  %s\n", indent(c.Body))
		if c.Original != "" {
			fmt.Fprintf(b, "  (the human rewrote a reviewer's comment, which said: %s)\n", indent(c.Original))
		}
		for _, r := range all {
			if r.ReplyTo == c.ID {
				fmt.Fprintf(b, "  > %s: %s\n", who(r), indent(r.Body))
			}
		}
	}
}

// where names the lines a comment is about, as the file stands after the
// change unless every one was removed.
func where(c event.ReviewComment) string {
	at := fmt.Sprintf("%s:%d", c.Path, c.Start)
	if c.End > c.Start {
		at += fmt.Sprintf("-%d", c.End)
	}
	if c.Side == "old" {
		at += " (removed lines, numbered as before the change)"
	}
	return at
}

func who(c event.ReviewComment) string {
	if c.Author == "" {
		return "the human"
	}
	return "reviewer " + c.Author
}

// indent keeps a multi-line body under the line it starts on.
func indent(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n  ") }
