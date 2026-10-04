package logging

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// Watch writes one record per fact and returns a stop func. Correlation
// ids come off the event, so no call site has to thread them.
func Watch(bus *event.Bus) func() {
	// Bus, not the publisher: the bus does not know who that was.
	log := For(Bus)
	// The stop waits for the last record to be written, not merely
	// received: a log that loses its tail at exit is worse than a slow one.
	return bus.Handle(worthKeeping, func(rec event.Record) {
		// A new session files its records, this one included, under its own id.
		if v, ok := rec.Event.(event.SessionStarted); ok && v.Session != uuid.Nil {
			current.Store(v.Session.String())
		}
		level, fields := describe(rec.Event)
		log.Log(context.Background(), level, string(rec.Event.Kind()),
			append([]any{KeyEvent, string(rec.Event.Kind()), KeyOrdinal, rec.Ordinal}, fields...)...)
	})
}

// worthKeeping takes every fact but live output, which is too noisy and
// arrives whole in ToolCallEnded or UserCommandEnded anyway.
func worthKeeping(e event.Event) bool {
	return !e.Kind().IsIntent() && e.Kind() != event.OutputChunkKind
}

// describe is the one place a fact becomes fields. Output and prose
// go through Body, which withholds them unless log_bodies is set.
func describe(e event.Event) (slog.Level, []any) {
	switch v := e.(type) {
	case event.SessionStarted:
		return slog.LevelInfo, []any{"model", v.Model, "sandbox", v.Sandbox,
			"network", v.Network, "max_steps", v.MaxSteps,
			"recorded", v.Recorded, "resumed", v.Resumed}
	case event.SessionResumed:
		return slog.LevelInfo, []any{"records", v.Records, "sandbox", v.Sandbox}
	case event.TurnStarted:
		return slog.LevelInfo, []any{KeyTurn, v.Turn, "n", v.N, "prompt", Body(v.Prompt)}
	case event.TurnEnded:
		return slog.LevelInfo, []any{KeyTurn, v.Turn, KeyReason, string(v.Reason),
			"tokens", v.Usage.Tokens(), KeyMS, v.Usage.Latency.Milliseconds()}
	case event.CheckpointTaken:
		return slog.LevelDebug, []any{KeyTurn, v.Turn, "snapshot", v.Snapshot}
	case event.BoundReached:
		return slog.LevelWarn, []any{KeyTurn, v.Turn, "steps", v.Steps}
	case event.RolledBack:
		return slog.LevelInfo, []any{KeyTurn, v.Turn, "revert_files", v.RevertFiles}
	case event.StepStarted:
		return slog.LevelDebug, []any{KeyTurn, v.Turn, KeyStep, v.Step, "n", v.N}
	case event.StepEnded:
		return slog.LevelInfo, []any{KeyTurn, v.Turn, KeyStep, v.Step, "tool_calls", v.ToolCalls,
			"stop", v.Stop, "tokens", v.Usage.Tokens(), "prompt_tokens", v.Usage.PromptTokens,
			"completion_tokens", v.Usage.CompletionTokens, KeyMS, v.Usage.Latency.Milliseconds()}
	case event.Appended:
		// A prompt or a note belongs to no Step, one between Turns to
		// no Turn, and a zero uuid in the field is worse than no field.
		var out []any
		if v.Turn != uuid.Nil {
			out = append(out, KeyTurn, v.Turn)
		}
		if v.Step != uuid.Nil {
			out = append(out, KeyStep, v.Step)
		}
		return slog.LevelDebug, append(out, "messages", len(v.Messages))
	case event.Compacted:
		// Info, not Debug: compaction rewrites the front of the transcript.
		return slog.LevelInfo, []any{KeyTurn, v.Turn, "dropped", v.Dropped, "note", Body(v.Note)}
	case event.ModelText:
		return slog.LevelDebug, []any{KeyStep, v.Step, "text", Body(v.Text)}
	case event.ToolCallProposed:
		// The command, not just the tool: most calls are bash, and a log
		// that cannot say what ran answers nothing.
		return slog.LevelInfo, []any{KeyToolCall, v.ToolCall, KeyStep, v.Step, "tool", v.Tool,
			"command", Body(event.Command(v.Tool, v.Args))}
	case event.ToolCallAssessed:
		return slog.LevelInfo, []any{KeyToolCall, v.ToolCall, "dangerous", v.Risk.Dangerous,
			"mutability", v.Risk.Mutability, "scope_risk", v.Risk.ScopeRisk,
			"from_judge", v.Risk.FromJudge, KeyReason, Snippet(v.Risk.Note)}
	case event.ApprovalAsked:
		// The same string the human was shown, since what they approved
		// is the whole of what this record is for.
		return slog.LevelInfo, []any{KeyToolCall, v.ToolCall, "tool", v.Tool,
			"command", Body(event.Command(v.Tool, v.Args)), KeyReason, Snippet(v.Rationale)}
	case event.ToolCallStarted:
		return slog.LevelDebug, []any{KeyToolCall, v.ToolCall, "runner", v.Runner}
	case event.ToolCallEnded:
		return level(v.Result.ExitCode != 0 || v.Result.Err != ""), []any{
			KeyToolCall, v.ToolCall, "exit", v.Result.ExitCode, KeyMS, v.Took.Milliseconds(),
			"bytes", len(v.Result.Stdout) + len(v.Result.Stderr),
			"stdout", Body(v.Result.Stdout), "stderr", Body(v.Result.Stderr),
			KeyReason, Snippet(v.Result.Err)}
	case event.ToolCallJudged:
		return slog.LevelDebug, []any{KeyToolCall, v.ToolCall, "status", v.Status,
			"kind", v.RenderKind, "attention", v.Attention,
			"goal_achieved", v.GoalAchieved, "from_judge", v.FromJudge}
	case event.ViewReady:
		return slog.LevelDebug, []any{KeyToolCall, v.ToolCall, "source", v.Source}
	case event.UserCommandStarted:
		// The command through Body like any other: it is the human's
		// content, not the harness's metadata.
		return slog.LevelInfo, []any{KeyUserCommand, v.UserCommand, "runner", v.Runner,
			"command", Body(v.Command)}
	case event.UserCommandEnded:
		return level(v.Result.ExitCode != 0 || v.Result.Err != ""), []any{
			KeyUserCommand, v.UserCommand, "exit", v.Result.ExitCode, KeyMS, v.Took.Milliseconds(),
			"bytes", len(v.Result.Stdout) + len(v.Result.Stderr),
			"stdout", Body(v.Result.Stdout), "stderr", Body(v.Result.Stderr),
			KeyReason, Snippet(v.Result.Err)}
	case event.Notice:
		// severity, not level: slog writes its own "level" and the last one wins.
		// Snippet, since an endpoint's error body may echo the request.
		return level(v.Level == "error" || v.Level == "warn"), []any{"severity", v.Level, "text", Snippet(v.Text)}
	case event.ContextMeasured:
		return slog.LevelDebug, []any{"total", v.Total, "budget", v.Budget, "exact", v.Exact}
	case event.SessionsListed:
		return slog.LevelDebug, []any{"sessions", len(v.Sessions)}
	case event.ServersListed:
		return slog.LevelDebug, []any{"servers", len(v.Servers)}
	case event.AuthorizationWaiting:
		// Not the URL: it carries the sign-in's state.
		return slog.LevelInfo, []any{"server", v.Server, "until", v.Until}
	case event.ServerAuthorized:
		return slog.LevelInfo, []any{"server", v.Server}
	case event.AuthorizationFailed:
		return slog.LevelWarn, []any{"server", v.Server, KeyReason, Snippet(v.Reason)}
	}
	return slog.LevelDebug, nil
}

func level(bad bool) slog.Level {
	if bad {
		return slog.LevelWarn
	}
	return slog.LevelInfo
}
