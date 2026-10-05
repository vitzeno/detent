package main

import (
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// spawning is what subagents add to a session when they are on: the tool,
// the root's rule to use it, and the engine's child model and limits.
type spawning struct {
	tools  []tool.Tool
	root   []model.ClientOption
	engine []engine.Option
}

// subagents is all of spawning or none of it: a spawn_agent with no child
// model can only answer that there are none. child speaks a subagent's prompt.
func subagents(cfg config.Config, child func(...model.ClientOption) *model.Client) (spawning, error) {
	if !cfg.SpawnsSubagents() {
		return spawning{}, nil
	}
	timeout, err := cfg.ChildTimeoutDuration()
	if err != nil {
		return spawning{}, err
	}
	return spawning{
		tools: []tool.Tool{tool.SpawnAgent{}},
		root:  []model.ClientOption{model.WithSubagents()},
		engine: []engine.Option{
			engine.WithChildModel(child(model.WithRole(model.RoleChild))),
			engine.WithAgentLimits(cfg.MaxAgents, cfg.ChildContextTokens, timeout),
		},
	}, nil
}
