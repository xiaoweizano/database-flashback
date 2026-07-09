package ws

// Command type constants. Each represents a distinct operation that the
// platform can request from an agent, or that an agent can send to the
// platform.
const (
	CmdPreflight   = "preflight"
	CmdPITRParse   = "pitr_parse"
	CmdPITRExecute = "pitr_execute"
	CmdStatus      = "status"
	CmdShutdown    = "shutdown"
)

// Response status constants.
const (
	StatusOK    = "ok"
	StatusError = "error"
)

// Command is a request sent over the WebSocket connection. Cmd holds a unique
// identifier (typically a UUID) used to correlate requests with responses.
// Type selects the handler on the receiving side. Params carries
// operation-specific payload.
type Command struct {
	Cmd    string                 `json:"cmd"`
	Type   string                 `json:"type"`
	Params map[string]interface{} `json:"params"`
}

// Response carries the result of a command execution. Cmd echoes the
// identifier from the corresponding Command so the caller can correlate them.
// Status is either "ok" or "error".
type Response struct {
	Cmd    string      `json:"cmd"`
	Status string      `json:"status"`
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}
