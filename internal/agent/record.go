package agent

import (
	"fmt"

	"github.com/vitzeno/detent/internal/propose"
)

// The Record* family closes a goal (or notes a standalone action) by
// appending a transcript message and, except RecordFileSave, marking
// res finished via finish.

func (s *Session) RecordDone(res *GoalResult, p propose.Proposal) {
	res.Summary = p.Summary
	s.append(propose.Message{Role: propose.RoleAssistant,
		Content: propose.EncodeAssistantTurn(p)})
	s.finish(res, EndDone, p.Summary)
}

// RecordFileSave notes a direct editor write (not a proposed command).
// Not goal-scoped: this happens outside RunGoal/BeginGoal entirely.
func (s *Session) RecordFileSave(path, diff string) {
	s.append(propose.Message{Role: propose.RoleTool, Content: fmt.Sprintf(
		"The human edited `%s` directly in the editor and saved this change:\n%s", path, boundStr(diff))})
}

func (s *Session) RecordDecline(res *GoalResult, command string) {
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended by human: declined command `%s` after %d command(s)]",
		command, len(res.Commands))})
	s.finish(res, EndDeclined, "")
}

func (s *Session) RecordBudget(res *GoalResult, budget int) {
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended: step budget exhausted (%d/%d), goal not confirmed done]", budget, budget)})
	s.finish(res, EndBudget, "")
}

func (s *Session) RecordProposerError(res *GoalResult, err error) {
	s.append(propose.Message{Role: propose.RoleUser, Content: fmt.Sprintf(
		"[goal ended: proposer error: %v]", err)})
	s.finish(res, EndProposerError, "")
}

// RecordAbort notes the human aborted. res is nil if BeginGoal hadn't
// produced one yet.
func (s *Session) RecordAbort(res *GoalResult) {
	s.append(propose.Message{Role: propose.RoleUser, Content: "[goal ended by human: aborted]"})
	if res == nil {
		return
	}
	s.finish(res, EndAborted, "")
}
