package mcp

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/vitzeno/detent/event"
)

// signInMax is a backstop for a human who walked away, not a deadline
// on signing in. esc stops a tool call waiting on one sooner.
const signInMax = 10 * time.Minute

var errSignInExpired = errors.New("the sign-in link expired")

// SignIns is every sign-in waiting on a human. It publishes the link
// and waits for the browser to come back. It never opens one unasked.
type SignIns struct {
	bus    *event.Bus
	in     *Invokers
	tokens Tokens
	open   func(string) error
	wait   time.Duration

	mu    sync.Mutex
	live  map[string]string // server to the link waiting now
	asked map[string]bool   // server to whether this sign-in showed one
	busy  map[string]bool   // server to whether a dial of it is under way

	// saving orders a token's save against forgetting it, and gen counts
	// the sign-ins a server has had, so a retired one never saves.
	saving sync.Mutex
	gen    map[string]int
}

// NewSignIns takes how to open a link rather than doing it itself, so
// ui runs no process and a test needs no browser. in may be nil.
func NewSignIns(bus *event.Bus, in *Invokers, tokens Tokens, open func(string) error) *SignIns {
	return &SignIns{bus: bus, in: in, tokens: tokens, open: open, wait: signInMax,
		live: map[string]string{}, asked: map[string]bool{}, busy: map[string]bool{}, gen: map[string]int{}}
}

// Open opens a waiting link in a browser, for OpenAuthorization.
func (s *SignIns) Open(server string) error {
	s.mu.Lock()
	link := s.live[server]
	s.mu.Unlock()
	if link == "" {
		return fmt.Errorf("no sign-in is waiting for %s", server)
	}
	if err := openable(link); err != nil {
		return err
	}
	return s.open(link)
}

// wasAsked reports whether the last sign-in for server showed a link.
func (s *SignIns) wasAsked(server string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked[server]
}

// claim reserves a server for one dial, so a redial cannot race the
// startup dial or another redial. False means one is under way.
func (s *SignIns) claim(server string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy[server] {
		return false
	}
	s.busy[server] = true
	return true
}

func (s *SignIns) release(server string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.busy, server)
}

func (s *SignIns) generation(server string) int {
	s.saving.Lock()
	defer s.saving.Unlock()
	return s.gen[server]
}

// save writes a token unless a later sign-in has retired gen.
func (s *SignIns) save(server, resource string, gen int, saved *Saved) error {
	s.saving.Lock()
	defer s.saving.Unlock()
	if s.gen[server] != gen {
		return fmt.Errorf("mcp: %s: a newer sign-in replaced this one", server)
	}
	return s.tokens.Save(server, resource, saved)
}

// retire forgets a server's token, and stops every earlier session saving one.
func (s *SignIns) retire(server, resource string) error {
	s.saving.Lock()
	defer s.saving.Unlock()
	s.gen[server]++
	return s.tokens.Forget(server, resource)
}

// publish is a no-op with no bus, as for the -mcp listing.
func (s *SignIns) publish(e event.Event) {
	if s.bus != nil {
		s.bus.Publish(e)
	}
}

// openable allows https, or http to this machine. The address comes
// from the server's own metadata, and "open" launches whatever it names.
func openable(link string) error {
	u, err := url.Parse(link)
	if err != nil {
		return fmt.Errorf("the sign-in link: %w", err)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && (host == "localhost" || (ip != nil && ip.IsLoopback())):
		return nil
	}
	return fmt.Errorf("refusing a sign-in link to %s://%s: only https is opened", u.Scheme, host)
}

// fetcher is the SDK's browser leg: publish the link, then wait for
// the redirect on loopback, the context, or the bound.
func (s *SignIns) fetcher(server string, port int, fixed bool) auth.AuthorizationCodeFetcher {
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		// Nothing to show a link on: the -mcp listing, or a test.
		if s.bus == nil {
			return nil, fmt.Errorf("%s needs signing in, and nothing here can show the link", server)
		}
		if err := openable(args.URL); err != nil {
			return nil, err
		}
		want, err := stateOf(args.URL)
		if err != nil {
			return nil, err
		}
		// Loopback only: served on every interface, the redirect hands
		// the code to the network.
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		ln, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
		switch {
		case err != nil && fixed:
			return nil, fmt.Errorf("listening for the sign-in: %w; free port %d or change callbackPort", err, port)
		case err != nil:
			return nil, fmt.Errorf("listening for the sign-in: %w; /mcp auth %s tries another port", err, server)
		}
		// A human is not held to how long a server may take to answer.
		if p := patienceFrom(ctx); p != nil {
			p.pause()
			defer p.resume()
		}
		back := make(chan callback, 1)
		srv := &http.Server{Handler: s.callback(server, addr, want, back), ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = srv.Serve(ln) }()
		defer func() { _ = srv.Close() }() // the code has arrived or never will

		s.waiting(server, args.URL)
		defer s.waiting(server, "")
		timer := time.NewTimer(s.wait)
		defer timer.Stop()
		select {
		case got := <-back:
			return got.result, got.err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, errSignInExpired
		}
	}
}

// callback is what came back on the redirect.
type callback struct {
	result *auth.AuthorizationResult
	err    error
}

// callback answers the browser. A request that is not this sign-in's
// is refused and the wait goes on, so a stray one cannot end it.
func (s *SignIns) callback(server, host, want string, back chan<- callback) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Host != host || q.Get("state") != want {
			http.Error(w, "This is not the sign-in detent is waiting for.", http.StatusBadRequest)
			return
		}
		got := callback{result: &auth.AuthorizationResult{Code: q.Get("code"), State: want, Iss: q.Get("iss")}}
		page := "Signed in to " + server + ". You can go back to the terminal."
		if e := q.Get("error"); e != "" {
			got = callback{err: fmt.Errorf("%s refused the sign-in: %s", server, e)}
			page = "Not signed in to " + server + ": " + e + "."
		}
		select {
		case back <- got:
		default:
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>detent</title><p>%s</p>\n", html.EscapeString(page))
	})
	return mux
}

// waiting publishes the link, or forgets it when link is "".
func (s *SignIns) waiting(server, link string) {
	s.mu.Lock()
	if link == "" {
		delete(s.live, server)
		s.mu.Unlock()
		return
	}
	s.live[server], s.asked[server] = link, true
	s.mu.Unlock()
	s.status(server, event.AuthWaiting)
	s.publish(event.AuthorizationWaiting{Server: server, URL: link, Until: time.Now().Add(s.wait)})
}

// begin and end bracket one Authorize, so a sign-in that showed a link
// always says how it ended.
func (s *SignIns) begin(server string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked[server] = false
}

func (s *SignIns) end(server string, err error) {
	s.mu.Lock()
	asked := s.asked[server]
	s.mu.Unlock()
	if !asked {
		return
	}
	if err != nil {
		s.status(server, event.AuthSignedOut)
		s.publish(event.AuthorizationFailed{Server: server, Reason: reason(err)})
		return
	}
	s.status(server, event.AuthSignedIn)
	s.publish(event.ServerAuthorized{Server: server})
}

func (s *SignIns) status(server, auth string) {
	if s.in == nil {
		return
	}
	s.in.setAuth(server, auth)
	s.publish(event.ServersListed{Servers: s.in.Status()})
}

func reason(err error) string {
	switch {
	case errors.Is(err, errSignInExpired):
		return "the link expired"
	case errors.Is(err, context.Canceled):
		return "stopped"
	}
	return err.Error()
}

// stateOf is the state the redirect must carry back.
func stateOf(link string) (string, error) {
	u, err := url.Parse(link)
	if err != nil {
		return "", fmt.Errorf("the sign-in link: %w", err)
	}
	state := u.Query().Get("state")
	if state == "" {
		return "", errors.New("the sign-in link carries no state")
	}
	return state, nil
}
