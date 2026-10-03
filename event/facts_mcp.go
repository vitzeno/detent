package event

import "time"

// ServersListed answers ListServers, failures included: a missing
// server is the thing a human needs told.
type ServersListed struct {
	fact
	Servers []ServerSummary `json:"Servers"`
}

func (ServersListed) Kind() Kind { return ServersListedKind }

// ServerSummary is one MCP server as configured, connected or not.
type ServerSummary struct {
	Name    string `json:"Name"`
	Command string `json:"Command"`
	// Connected tells a server still being dialled from one that
	// answered and offers nothing, which look alike from Tools alone.
	Connected bool `json:"Connected"`
	// Tools is how many it offered, once connected.
	Tools int `json:"Tools"`
	// Err is why it is not connected, "" when it is.
	Err string `json:"Err"`
	// Disabled is a server left in the config but switched off.
	Disabled bool `json:"Disabled"`
	// Auth is "" for a server with no auth, else one of the Auth values.
	Auth string `json:"Auth"`
}

// Auth is where a server's sign-in stands, for one that has auth.
const (
	AuthSignedIn  = "signed in"
	AuthWaiting   = "waiting"
	AuthSignedOut = "signed out"
)

// AuthorizationWaiting says a server wants a human to sign in, at URL.
// Until is when the wait gives up. The URL holds no secret.
type AuthorizationWaiting struct {
	fact
	Server string    `json:"Server"`
	URL    string    `json:"URL"`
	Until  time.Time `json:"Until"`
}

func (AuthorizationWaiting) Kind() Kind { return AuthorizationWaitingKind }

// ServerAuthorized says a sign-in worked: a token was got and saved.
type ServerAuthorized struct {
	fact
	Server string `json:"Server"`
}

func (ServerAuthorized) Kind() Kind { return ServerAuthorizedKind }

// AuthorizationFailed says a sign-in ended with no token: it expired,
// was stopped, or was refused. Reason is for a human.
type AuthorizationFailed struct {
	fact
	Server string `json:"Server"`
	Reason string `json:"Reason"`
}

func (AuthorizationFailed) Kind() Kind { return AuthorizationFailedKind }
