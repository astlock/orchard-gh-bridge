package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/breakawaydata/orchard-gh-bridge/orchard"
)

// hostnamePollInterval is how often the bridge checks whether a new VM has
// been scheduled onto a worker. A variable so tests can shorten it.
var hostnamePollInterval = 2 * time.Second

// hostnameExecWait is passed to the controller's exec API: how long the
// controller itself waits for the VM to boot and accept SSH.
const hostnameExecWait = uint16(120)

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

// hostnameCommand sets all three macOS host names. The value comes from
// HostnameFor, so it needs no shell quoting. sudo -n fails at once instead of
// waiting for a password on images without passwordless sudo.
func hostnameCommand(hostname string) string {
	return fmt.Sprintf(`set -e
sudo -n scutil --set HostName %[1]s
sudo -n scutil --set LocalHostName %[1]s
sudo -n scutil --set ComputerName %[1]s
`, hostname)
}

// startHostnameLabel names the VM after its worker in the background. It is
// best effort: the runner does not wait for it, and a failure only means the
// job runs under the image's hostname.
func (b *Bridge) startHostnameLabel(ctx context.Context, vmName string) {
	go func() {
		if err := b.labelHostname(ctx, vmName); err != nil {
			b.logger.Warn("could not name VM after its worker", "vm", vmName, "error", err)
		}
	}()
}

// labelHostname waits until vmName is scheduled onto a worker, then makes one
// attempt to set its hostname. It stops without error once the VM is gone.
func (b *Bridge) labelHostname(ctx context.Context, vmName string) error {
	for {
		vm, err := b.orchardClient.GetVM(ctx, vmName)
		switch {
		case errors.Is(err, orchard.ErrNotFound):
			return nil
		case err != nil:
			b.logger.Debug("checking VM placement", "vm", vmName, "error", err)
		case vm.Status == orchard.VMStatusFailed || vm.Status == orchard.VMStatusStopped:
			return nil
		case vm.Worker != "":
			hostname := HostnameFor(vm.Worker, vmName)
			res, err := b.orchardClient.Exec(ctx, vmName, hostnameCommand(hostname), hostnameExecWait)
			if err != nil {
				return err
			}
			if res.ExitCode != 0 {
				return fmt.Errorf("exit code %d: %s", res.ExitCode, strings.TrimSpace(res.Output))
			}
			b.logger.Info("named VM after its worker", "vm", vmName, "worker", vm.Worker, "hostname", hostname)
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(hostnamePollInterval):
		}
	}
}
