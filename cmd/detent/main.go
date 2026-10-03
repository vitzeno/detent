// Command detent runs the full-screen TUI, or one request headlessly with
// -prompt. It wires the engine, the bus, and whichever front-end.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/logging"
	"github.com/vitzeno/detent/ui"
	"github.com/vitzeno/detent/ui/theme"
	"github.com/vitzeno/detent/version"
)

func main() {
	err := run()
	if err == nil {
		return
	}
	code := 1
	var ended endedError
	if errors.As(err, &ended) {
		code = ended.code()
	}
	if msg := err.Error(); msg != "" {
		fmt.Fprintln(os.Stderr, "detent:", msg)
	}
	os.Exit(code)
}

// run reads top to bottom in the order a session opens. Each phase fills
// in sd as it goes, so an early return closes only what was reached.
func run() error {
	o := parseFlags()
	switch {
	case o.version:
		fmt.Println("detent", version.String())
		return nil
	case o.sessions:
		return listSessions()
	}
	if err := o.check(); err != nil {
		return err
	}

	trusted, cfg, err := configure(o)
	if err != nil {
		return err
	}
	// Before the ping and the log, which a command that only answers has no use for.
	switch {
	case o.prune:
		return pruneSandbox(cfg.SandboxSocket)
	case o.listMCP:
		return listServers(trusted)
	}
	// Validated even for -prompt, so a typo fails fast either way.
	if err := applyLook(cfg); err != nil {
		return err
	}
	// Waited for once the sandbox is up, so the two overlap.
	pinged := ping(cfg)

	// A resumed session keeps its id, so its records continue the same
	// log rather than starting a second one beside it.
	id, restore, err := openSession(o.resume)
	if err != nil {
		return err
	}
	s := &session{o: o, cfg: cfg, trusted: trusted, id: id, restore: restore}
	s.warnTrust()
	// A session that cannot log is still a session: Setup says so and
	// carries on discarding.
	closeLog, logErr := logging.Setup(id.String(),
		logging.WithDir(cfg.LogDir),
		logging.WithLevel(cfg.LogLevel),
		logging.WithBodies(cfg.LogsBodies()))
	s.warn(logErr)
	defer func() { _ = closeLog() }()
	// Deferred after the log, so the log outlives everything it closes.
	s.sd.session = id
	defer func() { s.sd.close() }()

	if err := s.openSandbox(); err != nil {
		return err
	}
	if err := <-pinged; err != nil {
		return fmt.Errorf("%w\n\nis the model endpoint up? Wanted %s with model %s. Check the key, or point -url/-model (or a config file) somewhere else. For a local LM Studio, load the model and Start Server",
			err, cfg.BaseURL, cfg.Model)
	}
	if err := s.buildEngine(); err != nil {
		return err
	}
	ctx := s.wire()
	if o.prompt != "" {
		return s.runHeadless(ctx)
	}
	return s.runTUI(ctx)
}

// applyLook checks the config and applies its theme before anything draws.
func applyLook(cfg config.Config) error {
	th, ok := theme.Themes[cfg.Theme]
	if !ok {
		return fmt.Errorf("unknown theme %q, choose one of: %s", cfg.Theme, strings.Join(theme.Names(), ", "))
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	theme.Apply(th)
	ui.RefreshStyles()
	return nil
}

// ping checks the endpoint in the background. Waited for after the
// sandbox, so startup still fails fast but the two overlap.
func ping(cfg config.Config) <-chan error {
	pinged := make(chan error, 1)
	go func() {
		pinged <- (&model.Client{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Headers: cfg.Headers}).Ping(context.Background())
	}()
	return pinged
}

// endedError is a headless request that ended other than done, so a
// script driving -prompt can tell finished from gave up.
type endedError event.EndReason

func (e endedError) Error() string {
	if event.EndReason(e) == event.EndError {
		return "the request failed"
	}
	// The reason is already printed, so nothing more to say.
	return ""
}

func (e endedError) code() int {
	switch event.EndReason(e) {
	case event.EndAborted:
		return 130
	case event.EndBound:
		return 3
	case event.EndStopped:
		return 4
	default:
		return 1
	}
}
