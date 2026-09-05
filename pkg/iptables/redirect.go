// Package iptables manages the nat PREROUTING REDIRECT rules that hand
// forwarded traffic to Envoy's listeners.
//
// These are the rules the README used to ask operators to add by hand. Managing
// them here keeps them in step with -https-port/-http-port and removes them
// again on shutdown, so a stopped proxy does not leave the box black-holing
// every client's web traffic.
package iptables

import (
	"fmt"
	"os/exec"
	"strconv"

	"github.com/sirupsen/logrus"
)

// DefaultBinary matches what smart-vpn-client uses on the same hosts. The
// legacy binary writes to the same tables the kernel's iptables_nat module
// serves, which nft-backed iptables does not always do on older kernels.
const DefaultBinary = "iptables-legacy"

// Rule redirects TCP traffic destined for DestPort to a local ToPort.
type Rule struct {
	DestPort int
	ToPort   int
}

func (r Rule) args(action string) []string {
	return []string{
		"-t", "nat",
		action, "PREROUTING",
		"-p", "tcp",
		"--dport", strconv.Itoa(r.DestPort),
		"-j", "REDIRECT",
		"--to-port", strconv.Itoa(r.ToPort),
	}
}

func (r Rule) String() string {
	return fmt.Sprintf("tcp/%d -> 127.0.0.1:%d", r.DestPort, r.ToPort)
}

// runner executes the iptables binary. Swapped out in tests.
type runner func(args ...string) ([]byte, error)

// Manager owns a set of redirect rules.
type Manager struct {
	rules []Rule
	run   runner
}

// NewManager builds a Manager driving the given iptables binary.
func NewManager(binary string, rules []Rule) *Manager {
	if binary == "" {
		binary = DefaultBinary
	}
	return &Manager{
		rules: rules,
		run: func(args ...string) ([]byte, error) {
			return exec.Command(binary, args...).CombinedOutput()
		},
	}
}

// exists reports whether the rule is already installed. A non-zero exit from
// -C means "absent", not "failed", so the error is deliberately swallowed.
func (m *Manager) exists(r Rule) bool {
	_, err := m.run(r.args("-C")...)
	return err == nil
}

// Ensure idempotently installs every rule. Safe to call repeatedly.
func (m *Manager) Ensure() error {
	for _, r := range m.rules {
		if m.exists(r) {
			logrus.Debugf("Redirect already present: %s", r)
			continue
		}
		if out, err := m.run(r.args("-A")...); err != nil {
			return fmt.Errorf("adding redirect %s: %s (%s)", r, err, out)
		}
		logrus.Infof("Installed redirect %s", r)
	}
	return nil
}

// Remove deletes every rule. Errors are logged rather than returned: this runs
// on the shutdown path, where a missing rule is the desired end state anyway.
func (m *Manager) Remove() {
	for _, r := range m.rules {
		if !m.exists(r) {
			continue
		}
		if out, err := m.run(r.args("-D")...); err != nil {
			logrus.Infof("Failed to remove redirect %s: %s (%s)", r, err, out)
			continue
		}
		logrus.Infof("Removed redirect %s", r)
	}
}
