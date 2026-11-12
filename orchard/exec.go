package orchard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/coder/websocket"
)

// maxExecOutput caps how much command output Exec keeps; the rest is dropped.
const maxExecOutput = 64 << 10

// ExecResult is the outcome of a command run in a VM through the controller.
type ExecResult struct {
	ExitCode int
	// Output holds stdout and stderr interleaved, truncated to 64 KiB.
	Output string
}

// execFrame mirrors the controller's exec stream frame (internal/execstream
// in Orchard), which is not importable.
type execFrame struct {
	Type string `json:"type"`
	Data []byte `json:"data,omitempty"`
	Exit *struct {
		Code int32 `json:"code"`
	} `json:"exit,omitempty"`
	Error string `json:"error,omitempty"`
}

// Exec runs command in the VM over SSH via the controller's exec API
// (GET /v1/vms/{name}/exec, compute:write). The controller waits up to
// waitSeconds for the VM to be running and reachable before it gives up.
func (c *officialClient) Exec(ctx context.Context, name, command string, waitSeconds uint16) (*ExecResult, error) {
	conn, err := c.inner.VMs().Exec(ctx, name, command, false, waitSeconds)
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("VM %q: %w", name, ErrNotFound)
		}
		return nil, fmt.Errorf("exec in VM %s: %w", name, err)
	}
	defer func() { _ = conn.CloseNow() }()

	return readExecFrames(func() ([]byte, error) {
		_, data, err := conn.Read(ctx)
		return data, err
	})
}

// readExecFrames consumes exec frames until the command's exit status arrives.
func readExecFrames(next func() ([]byte, error)) (*ExecResult, error) {
	var out strings.Builder
	for {
		data, err := next()
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return nil, errors.New("exec stream closed without an exit status")
			}
			return nil, fmt.Errorf("reading exec stream: %w", err)
		}

		var frame execFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			return nil, fmt.Errorf("decoding exec frame: %w", err)
		}

		switch frame.Type {
		case "stdout", "stderr":
			if room := maxExecOutput - out.Len(); room > 0 {
				if len(frame.Data) > room {
					frame.Data = frame.Data[:room]
				}
				out.Write(frame.Data)
			}
		case "exit":
			if frame.Exit == nil {
				return nil, errors.New("exec exit frame without a code")
			}
			return &ExecResult{ExitCode: int(frame.Exit.Code), Output: out.String()}, nil
		case "error":
			return nil, fmt.Errorf("exec failed: %s", frame.Error)
		}
	}
}
