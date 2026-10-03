package main

// The flags that answer and exit, rather than starting a session.
// main.go is left holding the wiring.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	mcppkg "github.com/vitzeno/detent/internal/mcp"
	"github.com/vitzeno/detent/internal/sandbox"
	"github.com/vitzeno/detent/internal/store"
	"github.com/vitzeno/detent/internal/tool"
)

func listSessions() error {
	events, err := store.Open(store.DefaultPath())
	if err != nil {
		return err
	}
	defer func() { _ = events.Close() }()

	all, err := events.Sessions()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		fmt.Println("no sessions recorded yet")
		return nil
	}
	for _, s := range all {
		name := s.Model
		if s.Name != "" {
			name = s.Name
		}
		fmt.Printf("%s  %s  %-12s %s\n", s.ID,
			s.Started.Local().Format("2006-01-02 15:04"),
			fmt.Sprintf("%d events", s.Events), name)
	}
	return nil
}

// listServers connects, says what each offers, and exits. Worth its
// own flag: a server that answers here is one the model will see.
func listServers() error {
	cfg, err := mcppkg.Load(mcppkg.Files()...)
	if err != nil {
		return err
	}
	if len(cfg) == 0 {
		fmt.Printf("no mcp servers configured, looked in %s\n", strings.Join(mcppkg.Files(), " and "))
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reg, servers := tool.Standard(), mcppkg.NewInvokers()
	errs := mcppkg.ConnectAll(ctx, reg, servers, cfg, nil)
	defer func() { _ = bounded(serverGrace, "mcp servers", servers.Close) }()

	for _, s := range servers.Servers() {
		tools, err := s.Tools(ctx)
		if err != nil {
			fmt.Printf("%s: %v\n", s.Name, err)
			continue
		}
		fmt.Printf("%s  %d tool(s)\n", s.Name, len(tools))
		for _, t := range tools {
			fmt.Printf("    %s\n", t.Name)
		}
	}
	for _, err := range errs {
		fmt.Fprintln(os.Stderr, err)
	}
	if len(servers.Servers()) == 0 {
		return fmt.Errorf("no mcp server connected")
	}
	return nil
}

// pruneSandbox reports what it removed rather than saying nothing,
// because deleting someone's containers silently is the wrong default.
func pruneSandbox(socket string) error {
	if socket == "" {
		return fmt.Errorf("no default containerd socket for this OS: set -sandbox-socket (or sandbox_socket in config)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := sandbox.Preflight(ctx, socket); err != nil {
		return fmt.Errorf("%w\n\nis containerd reachable at %s?", err, socket)
	}
	out, err := sandbox.Prune(ctx, socket, sandbox.DefaultNamespace)
	for _, id := range out.Containers {
		fmt.Println("removed container", id)
	}
	for _, id := range out.Leases {
		fmt.Println("removed lease for", id)
	}
	for _, id := range out.Kept {
		fmt.Println("still running, left alone:", id)
	}
	if err == nil && out.Empty() {
		fmt.Println("nothing to prune")
	}
	return err
}
