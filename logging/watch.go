package logging

import (
	"context"
	"github.com/google/uuid"
	"log/slog"

	"github.com/vitzeno/detent/event"
)

// Watch writes one record per fact and returns a stop func. This is
// the whole of how a session gets logged: correlation comes off the
// event, so no call site has to thread it and none can get it wrong.
func Watch(bus *event.Bus) func() {
	facts, unsub := bus.Subscribe(worthKeeping)
	log := For(Engine)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for rec := range facts {
			level, fields := describe(rec.Event)
			log.Log(context.TODO(), level, string(rec.Event.Kind()),
				append([]any{KeyEvent, string(rec.Event.Kind()), KeyOrdinal, rec.Ordinal}, fields...)...)
		}
	}()
	// The stop waits for the last record to be written, not merely
	// received: a log that loses its tail at exit is worse than a
	// slow one.
	return func() {
		unsub()
		<-done
	}
}

// worthKeeping takes every fact but live output: a line at a time is
// the one thing too noisy to keep, and CallEnded carries the whole of
// it anyway.
func worthKeeping(e event.Event) bool {
	return !e.Kind().IsIntent() && e.Kind() != event.OutputChunkKind
}

// describe is the one place a fact becomes fields. Output and prose
// go through Body, which withholds them unless log_bodies is set.
func describe(e event.Event) (slog.Level, []any) {
	switch v := e.(type) {
	case event.SessionStarted:
		return slog.LevelInfo, []any{"model", v.Model, "sandbox", v.Sandbox,
			"network", v.Network, "max_steps", v.MaxSteps}
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
		return slog.LevelInfo, []any{KeyTurn, v.Turn, KeyStep, v.Step, "calls", v.Calls,
			"tokens", v.Usage.Tokens(), KeyMS, v.Usage.Latency.Milliseconds()}
	case event.Appended:
		// A prompt or a note belongs to no Step, and a zero uuid in
		// the field is worse than no field.
		out := []any{KeyTurn, v.Turn, "messages", len(v.Messages)}
		if v.Step != uuid.Nil {
			out = append(out, KeyStep, v.Step)
		}
		return slog.LevelDebug, out
	case event.Compacted:
		// Info, not Debug: compaction rewrites the front of the
		// transcript and used to tell nobody at all.
		return slog.LevelInfo, []any{KeyTurn, v.Turn, "dropped", v.Dropped, "note", Body(v.Note)}
	case event.ModelText:
		return slog.LevelDebug, []any{KeyStep, v.Step, "text", Body(v.Text)}
	case event.CallProposed:
		return slog.LevelInfo, []any{KeyCall, v.Call, KeyStep, v.Step, "tool", v.Tool}
	case event.CallAssessed:
		return slog.LevelInfo, []any{KeyCall, v.Call, "dangerous", v.Risk.Dangerous,
			"mutability", v.Risk.Mutability, "scope_risk", v.Risk.ScopeRisk,
			"from_judge", v.Risk.FromJudge, KeyReason, v.Risk.Note}
	case event.ApprovalAsked:
		return slog.LevelInfo, []any{KeyCall, v.Call, "tool", v.Tool}
	case event.CallStarted:
		return slog.LevelDebug, []any{KeyCall, v.Call, "runner", v.Runner}
	case event.CallEnded:
		return level(v.Result.ExitCode != 0 || v.Result.Err != ""), []any{
			KeyCall, v.Call, "exit", v.Result.ExitCode, KeyMS, v.Took.Milliseconds(),
			"bytes", len(v.Result.Stdout) + len(v.Result.Stderr),
			"stdout", Body(v.Result.Stdout), "stderr", Body(v.Result.Stderr),
			KeyReason, v.Result.Err}
	case event.CallJudged:
		return slog.LevelDebug, []any{KeyCall, v.Call, "status", v.Status,
			"kind", v.RenderKind, "attention", v.Attention,
			"goal_achieved", v.GoalAchieved, "from_judge", v.FromJudge}
	case event.ViewReady:
		return slog.LevelDebug, []any{KeyCall, v.Call, "source", v.Source}
	case event.Notice:
		return level(v.Level == "error" || v.Level == "warn"), []any{"level", v.Level, "text", v.Text}
	}
	return slog.LevelDebug, nil
}

func level(bad bool) slog.Level {
	if bad {
		return slog.LevelWarn
	}
	return slog.LevelInfo
}
