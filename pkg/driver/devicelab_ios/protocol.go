// Package devicelab_ios is the host side of --driver devicelab on iOS. It
// drives the devicelab-ios-agent, an XCUITest agent that maestro-runner ships
// prebuilt (drivers/ios/devicelab-ios-agent/). The agent captures the screen,
// does coarse matching on the device and synthesizes input; this package
// applies Maestro's selector semantics to the candidates it returns, and uses
// simctl for everything outside the app (install, permissions, state).
//
// Wire protocol: see PROTOCOL.md in the agent repository.
package devicelab_ios

// ProtocolMajor is the agent protocol major version this host speaks.
const ProtocolMajor = "1"

// Args is the union of every command's arguments. Zero values are omitted.
type Args struct {
	App         string            `json:"app,omitempty"`
	MaxNodes    int               `json:"maxNodes,omitempty"`
	VisibleOnly bool              `json:"visibleOnly,omitempty"`
	Needles     []string          `json:"needles,omitempty"`
	IDNeedle    string            `json:"idNeedle,omitempty"`
	MinVisible  float64           `json:"minVisible,omitempty"`
	MaxResults  int               `json:"maxResults,omitempty"`
	Alerts      bool              `json:"alerts,omitempty"`
	DeadlineMs  float64           `json:"deadlineMs,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	X           *float64          `json:"x,omitempty"`
	Y           *float64          `json:"y,omitempty"`
	X2          *float64          `json:"x2,omitempty"`
	Y2          *float64          `json:"y2,omitempty"`
	HoldMs      *float64          `json:"holdMs,omitempty"`
	MoveMs      *float64          `json:"moveMs,omitempty"`
	RestMs      *float64          `json:"restMs,omitempty"`
	Count       int               `json:"count,omitempty"`
	Settle      bool              `json:"settle,omitempty"`
	Text        string            `json:"text,omitempty"`
	Key         string            `json:"key,omitempty"`
	Erase       int               `json:"erase,omitempty"`
	Speed       int               `json:"speed,omitempty"`
	Verify      *bool             `json:"verify,omitempty"`
	TimeoutMs   float64           `json:"timeoutMs,omitempty"`
	Quiescence  string            `json:"quiescence,omitempty"`
	Action      string            `json:"action,omitempty"`
	BundleID    string            `json:"bundleId,omitempty"`
	Arguments   []string          `json:"arguments,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Button      string            `json:"button,omitempty"`
	Value       string            `json:"value,omitempty"`
	Format      string            `json:"format,omitempty"`
	Quality     float64           `json:"quality,omitempty"`
}

// Node is one flattened accessibility node.
type Node struct {
	I           int     `json:"i"`
	P           int     `json:"p"`
	Type        string  `json:"t"`
	ID          string  `json:"id,omitempty"`
	Label       string  `json:"label,omitempty"`
	Value       string  `json:"value,omitempty"`
	Placeholder string  `json:"placeholder,omitempty"`
	Title       string  `json:"title,omitempty"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	W           float64 `json:"w"`
	H           float64 `json:"h"`
	Enabled     bool    `json:"en"`
	Selected    bool    `json:"sel"`
	Focused     bool    `json:"foc"`
	Vis         float64 `json:"vis"`
}

// Payload is a response's data; each command fills its own fields.
type Payload struct {
	Message     string   `json:"message,omitempty"`
	Version     string   `json:"version,omitempty"`
	UptimeMs    float64  `json:"uptimeMs,omitempty"`
	BundleID    string   `json:"bundleId,omitempty"`
	AppState    string   `json:"appState,omitempty"`
	ScreenW     float64  `json:"screenW,omitempty"`
	ScreenH     float64  `json:"screenH,omitempty"`
	Nodes       []Node   `json:"nodes,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
	Settled     *bool    `json:"settled,omitempty"`
	Signal      string   `json:"signal,omitempty"`
	WaitedMs    float64  `json:"waitedMs,omitempty"`
	ScreenHash  string   `json:"screenHash,omitempty"`
	Text        *string  `json:"text,omitempty"`
	Typed       bool     `json:"typed,omitempty"`
	Verified    *bool    `json:"verified,omitempty"`
	Repaired    bool     `json:"repaired,omitempty"`
	Present     bool     `json:"present,omitempty"`
	Title       string   `json:"title,omitempty"`
	Buttons     []string `json:"buttons,omitempty"`
	Visible     bool     `json:"visible,omitempty"`
	FrameX      float64  `json:"frameX,omitempty"`
	FrameY      float64  `json:"frameY,omitempty"`
	FrameW      float64  `json:"frameW,omitempty"`
	FrameH      float64  `json:"frameH,omitempty"`
	Image       string   `json:"image,omitempty"`
	Orientation string   `json:"orientation,omitempty"`
	Appearance  string   `json:"appearance,omitempty"`
}

// ErrorBody is a failed response's error.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Response is the agent's envelope.
type Response struct {
	OK         bool               `json:"ok"`
	ID         string             `json:"id,omitempty"`
	Data       *Payload           `json:"data,omitempty"`
	Error      *ErrorBody         `json:"error,omitempty"`
	ServerMs   float64            `json:"serverMs"`
	SnapshotID int                `json:"snapshotId"`
	Phases     map[string]float64 `json:"phases,omitempty"`
}

// payload returns the data, never nil.
func (r *Response) payload() *Payload {
	if r == nil || r.Data == nil {
		return &Payload{}
	}
	return r.Data
}

// AgentError is an `ok: false` answer.
type AgentError struct {
	Code    string
	Message string
}

func (e *AgentError) Error() string { return "agent: " + e.Code + ": " + e.Message }

// Agent error codes.
const (
	ErrSnapshotFailed = "SNAPSHOT_FAILED"
	ErrUnsupported    = "UNSUPPORTED"
	ErrNotFound       = "NOT_FOUND"
)

func f64(v float64) *float64 { return &v }
