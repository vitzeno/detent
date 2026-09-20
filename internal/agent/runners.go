package agent

// SingleRunner adapts one Runner into a RunnerSelector that always
// picks it. Sandbox always returns nil.
type SingleRunner struct{ Runner Runner }

func (s SingleRunner) Select(PreJudgment) (Runner, string) { return s.Runner, "" }

func (s SingleRunner) Probe() Runner { return s.Runner }

func (s SingleRunner) Sandbox() Runner { return nil }
