package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/breakawaydata/orchard-gh-bridge/orchard"
)

func TestHostnameFor(t *testing.T) {
	tests := []struct {
		worker, vm, want string
	}{
		{"mini-01", "gha-orchard-macos-tahoe-3f2a9c1d", "mini-01-3f2a9c1d"},
		{"Mini_07.local", "gha-x-abcd1234", "mini-07-local-abcd1234"},
		{"", "gha-x-abcd1234", "abcd1234"},
		{strings.Repeat("w", 80), "gha-x-abcd1234", strings.Repeat("w", 54) + "-abcd1234"},
	}
	for _, tt := range tests {
		got := HostnameFor(tt.worker, tt.vm)
		if got != tt.want {
			t.Errorf("HostnameFor(%q, %q) = %q, want %q", tt.worker, tt.vm, got, tt.want)
		}
		if len(got) > 63 {
			t.Errorf("HostnameFor(%q, %q) is %d chars, want <= 63", tt.worker, tt.vm, len(got))
		}
	}
}

func TestHostnameCommand(t *testing.T) {
	cmd := hostnameCommand("mini-01-3f2a9c1d")
	for _, want := range []string{
		"sudo -n scutil --set HostName mini-01-3f2a9c1d",
		"sudo -n scutil --set LocalHostName mini-01-3f2a9c1d",
		"sudo -n scutil --set ComputerName mini-01-3f2a9c1d",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command missing %q:\n%s", want, cmd)
		}
	}
}

func shortHostnamePoll(t *testing.T) {
	t.Helper()
	interval := hostnamePollInterval
	hostnamePollInterval = 5 * time.Millisecond
	t.Cleanup(func() { hostnamePollInterval = interval })
}

func hostnameTestBridge(mock *mockOrchardClient) *Bridge {
	return New(Config{ScaleSetName: "test", OrchardClient: mock, Capacity: NewCapacity(1), Logger: testLogger()})
}

func TestLabelHostname_WaitsForWorkerThenExecsOnce(t *testing.T) {
	shortHostnamePoll(t)
	mock := newMockOrchard()
	mock.setVM(orchard.VM{Name: "gha-orchard-test-3f2a9c1d", Status: orchard.VMStatusCreating})
	b := hostnameTestBridge(mock)

	go func() {
		time.Sleep(20 * time.Millisecond)
		mock.setVM(orchard.VM{Name: "gha-orchard-test-3f2a9c1d", Status: orchard.VMStatusCreating, Worker: "mini-01"})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := b.labelHostname(ctx, "gha-orchard-test-3f2a9c1d"); err != nil {
		t.Fatalf("labelHostname: %v", err)
	}

	calls := mock.execCalls()
	if len(calls) != 1 {
		t.Fatalf("exec calls = %d, want 1", len(calls))
	}
	if calls[0].vm != "gha-orchard-test-3f2a9c1d" {
		t.Errorf("exec VM = %q", calls[0].vm)
	}
	if !strings.Contains(calls[0].command, "HostName mini-01-3f2a9c1d") {
		t.Errorf("exec command does not set the hostname:\n%s", calls[0].command)
	}
}

func TestLabelHostname_OneAttemptOnly(t *testing.T) {
	shortHostnamePoll(t)
	for _, tc := range []struct {
		name    string
		result  *orchard.ExecResult
		err     error
		wantErr string
	}{
		{"exec error", nil, errors.New("ssh not ready"), "ssh not ready"},
		{"non-zero exit", &orchard.ExecResult{ExitCode: 1, Output: "sudo: a password is required\n"}, nil, "sudo: a password is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockOrchard()
			mock.setVM(orchard.VM{Name: "gha-orchard-test-1", Status: orchard.VMStatusRunning, Worker: "w1"})
			mock.execFn = func(int) (*orchard.ExecResult, error) { return tc.result, tc.err }
			b := hostnameTestBridge(mock)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := b.labelHostname(ctx, "gha-orchard-test-1")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if got := len(mock.execCalls()); got != 1 {
				t.Errorf("exec calls = %d, want exactly 1", got)
			}
		})
	}
}

func TestLabelHostname_StopsWhenVMIsGone(t *testing.T) {
	shortHostnamePoll(t)
	for _, tc := range []struct {
		name string
		vm   *orchard.VM
	}{
		{"deleted", nil},
		{"failed", &orchard.VM{Name: "gha-orchard-test-1", Status: orchard.VMStatusFailed}},
		{"stopped", &orchard.VM{Name: "gha-orchard-test-1", Status: orchard.VMStatusStopped, Worker: "w1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockOrchard()
			if tc.vm != nil {
				mock.setVM(*tc.vm)
			}
			b := hostnameTestBridge(mock)
			if err := b.labelHostname(context.Background(), "gha-orchard-test-1"); err != nil {
				t.Fatalf("labelHostname: %v", err)
			}
			if got := len(mock.execCalls()); got != 0 {
				t.Errorf("exec calls = %d, want 0", got)
			}
		})
	}
}

func TestLabelHostname_StopsWithBridgeContext(t *testing.T) {
	shortHostnamePoll(t)
	mock := newMockOrchard()
	mock.setVM(orchard.VM{Name: "gha-orchard-test-1", Status: orchard.VMStatusCreating})
	b := hostnameTestBridge(mock)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := b.labelHostname(ctx, "gha-orchard-test-1"); err != nil {
		t.Fatalf("labelHostname: %v", err)
	}
	if got := len(mock.execCalls()); got != 0 {
		t.Errorf("exec calls = %d, want 0", got)
	}
}
