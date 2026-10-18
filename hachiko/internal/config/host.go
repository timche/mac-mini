package config

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The tailnet's name for this Mac rather than the kernel hostname, which the router can
// rename under a running Mac: shibuya keys a Mac by this name, so a rename reads there as
// the Mac going offline. The tailnet name is the one a person sets in the admin console,
// and the one a replacement Mac is given, so it survives both.
var tailnetName = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tailscale", "status", "--json").Output()
	if err != nil {
		return "", err
	}
	var status struct {
		Self struct{ DNSName string }
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return "", err
	}
	label, _, _ := strings.Cut(status.Self.DNSName, ".")
	return label, nil
}

// The last name the tailnet gave is kept, so a sweep while tailscale is down or logged out
// still checks in as the same Mac. The kernel hostname is for a Mac never on a tailnet.
//
// Read here and written only by RememberHost, because every command loads its config and
// `hachiko status` and the dry runs may write nothing.
func machineName(stateDir string) (name string, tailnet bool) {
	if name, err := tailnetName(); err == nil && name != "" {
		return name, true
	}
	if old, err := os.ReadFile(filepath.Join(stateDir, "host")); err == nil {
		if name := strings.TrimSpace(string(old)); name != "" {
			return name, false
		}
	}
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	return host, false
}

// Keeps the name the tailnet gave this time for a sweep that cannot ask it, and is called
// by the check, which is the one thing that checks in under it.
func (c Config) RememberHost() error {
	if !c.hostFromTailnet {
		return nil
	}
	saved := filepath.Join(c.StateDir, "host")
	if old, _ := os.ReadFile(saved); strings.TrimSpace(string(old)) == c.Host {
		return nil
	}
	if err := os.MkdirAll(c.StateDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(saved, []byte(c.Host+"\n"), 0o644)
}
