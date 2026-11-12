package orchard

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func frameSource(t *testing.T, frames ...any) func() ([]byte, error) {
	t.Helper()
	var encoded [][]byte
	for _, f := range frames {
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, b)
	}
	return func() ([]byte, error) {
		if len(encoded) == 0 {
			return nil, websocket.CloseError{Code: websocket.StatusNormalClosure}
		}
		next := encoded[0]
		encoded = encoded[1:]
		return next, nil
	}
}

func TestReadExecFrames_ExitCodeAndOutput(t *testing.T) {
	res, err := readExecFrames(frameSource(t,
		map[string]any{"type": "stdout", "data": []byte("hello ")},
		map[string]any{"type": "stderr", "data": []byte("world")},
		map[string]any{"type": "exit", "exit": map[string]int{"code": 3}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.Output != "hello world" {
		t.Errorf("got %+v", res)
	}
}

func TestReadExecFrames_ErrorFrame(t *testing.T) {
	_, err := readExecFrames(frameSource(t, map[string]any{"type": "error", "error": "ssh: handshake failed"}))
	if err == nil || !strings.Contains(err.Error(), "ssh: handshake failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadExecFrames_ClosedWithoutExit(t *testing.T) {
	_, err := readExecFrames(frameSource(t, map[string]any{"type": "stdout", "data": []byte("x")}))
	if err == nil || !strings.Contains(err.Error(), "without an exit status") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadExecFrames_ReadError(t *testing.T) {
	_, err := readExecFrames(func() ([]byte, error) { return nil, errors.New("boom") })
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadExecFrames_TruncatesOutput(t *testing.T) {
	big := []byte(strings.Repeat("a", maxExecOutput+100))
	res, err := readExecFrames(frameSource(t,
		map[string]any{"type": "stdout", "data": big},
		map[string]any{"type": "stdout", "data": []byte("more")},
		map[string]any{"type": "exit", "exit": map[string]int{"code": 0}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Output) != maxExecOutput {
		t.Errorf("output length = %d, want %d", len(res.Output), maxExecOutput)
	}
}
