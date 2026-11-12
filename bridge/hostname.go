package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/breakawaydata/orchard-gh-bridge/orchard"
)

// hostInfoFile is written in the VM user's home once the hostname is set.
// The startup script waits for it and copies it into the runner's .env, so
// jobs see ORCHARD_WORKER.
const hostInfoFile = ".orchard-host.env"

// Hostname labelling timings. Variables so tests can shorten them.
var (
	hostnamePollInterval = 2 * time.Second
	hostnameDeadline     = 5 * time.Minute
	hostnameExecWait     = uint16(60)
)

// HostnameFor returns the VM hostname for a VM placed on worker:
// "<worker>-<last segment of the VM name>", lowercased, letters, digits and
// hyphens only, at most 63 characters (a valid macOS LocalHostName).
func HostnameFor(worker, vmName string) string {
	suffix := vmName
	if i := strings.LastIndex(vmName, "-"); i >= 0 {
		suffix = vmName[i+1:]
	}
	clean := func(s string) string {
		s = nonAlphanumHyphen.ReplaceAllString(strings.ToLower(s), "-")
		return strings.Trim(s, "-")
	}
	suffix = clean(suffix)
	host := clean(worker)
	if max := 63 - len(suffix) - 1; len(host) > max {
		host = strings.TrimRight(host[:max], "-")
	}
	if host == "" {
		return suffix
	}
	return host + "-" + suffix
}

// hostnameCommand sets all three macOS host names and then writes the host
// info file atomically. Both values come from HostnameFor and a sanitised
// worker name, so they need no shell quoting.
func hostnameCommand(hostname, worker string) string {
	return fmt.Sprintf(`set -e
sudo scutil --set HostName %[1]s
sudo scutil --set LocalHostName %[1]s
sudo scutil --set ComputerName %[1]s
printf 'ORCHARD_WORKER=%[2]s\n' > "$HOME/%[3]s.tmp"
mv "$HOME/%[3]s.tmp" "$HOME/%[3]s"
`, hostname, worker, hostInfoFile)
}

// startHostnameLabel names the VM after its worker in the background.
func (b *Bridge) startHostnameLabel(vmName string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), hostnameDeadline)
		defer cancel()
		if err := b.labelHostname(ctx, vmName); err != nil {
			b.logger.Warn("could not name VM after its worker", "vm", vmName, "error", err)
		}
	}()
}

// labelHostname waits until vmName is scheduled and running, then sets its
// hostname from the worker name. Exec errors are retried until ctx ends,
// because SSH comes up a little after the VM reports running.
func (b *Bridge) labelHostname(ctx context.Context, vmName string) error {
	var lastErr error
	for {
		vm, err := b.orchardClient.GetVM(ctx, vmName)
		switch {
		case errors.Is(err, orchard.ErrNotFound):
			return nil
		case err != nil:
			lastErr = err
		case vm.Status == orchard.VMStatusFailed || vm.Status == orchard.VMStatusStopped:
			return nil
		case vm.Worker != "" && vm.Status == orchard.VMStatusRunning:
			worker := nonAlphanumHyphen.ReplaceAllString(strings.ToLower(vm.Worker), "-")
			hostname := HostnameFor(vm.Worker, vmName)
			res, err := b.orchardClient.Exec(ctx, vmName, hostnameCommand(hostname, worker), hostnameExecWait)
			switch {
			case err != nil:
				lastErr = err
			case res.ExitCode != 0:
				lastErr = fmt.Errorf("exit code %d: %s", res.ExitCode, strings.TrimSpace(res.Output))
			default:
				b.logger.Info("named VM after its worker", "vm", vmName, "worker", vm.Worker, "hostname", hostname)
				return nil
			}
		}

		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			return lastErr
		case <-time.After(hostnamePollInterval):
		}
	}
}
