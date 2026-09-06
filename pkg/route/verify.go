package route

import (
	"fmt"
	"net"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

// DefaultProbe is the destination used for the route lookups. No packets are
// sent -- `ip route get` only asks the kernel which route it would pick -- so
// this just needs to be a public address that is not special-cased.
const DefaultProbe = "1.1.1.1"

// parseDev pulls the egress interface out of `ip route get` output, e.g.
//
//	1.1.1.1 from 172.16.0.90 via 172.16.0.1 dev eth0 table 200 src ...
//
// Returns "" when the output has no dev, which happens on lookup failures such
// as an unreachable destination.
func parseDev(out string) string {
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

// routeGet asks the kernel which interface it would use for probe, optionally
// from a specific source address.
func routeGet(probe string, from net.IP, mark int) (string, error) {
	args := []string{"route", "get", probe}
	if from != nil {
		args = append(args, "from", from.String())
	}
	if mark != 0 {
		args = append(args, "mark", fmt.Sprintf("%#x", mark))
	}
	out, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ip %s: %s (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	dev := parseDev(string(out))
	if dev == "" {
		return "", fmt.Errorf("no egress interface in: %s", strings.TrimSpace(string(out)))
	}
	return dev, nil
}

// selectorRuleExists reports whether any ip rule selects on the configured
// selector -- the fwmark when one is set, otherwise the bypass address --
// whoever installed it. Deliberately looser than ruleMatches: the operator may
// manage the rule themselves with a different table or priority, and the
// question here is only whether *something* steers this traffic.
func selectorRuleExists(ip net.IP, mark int) (bool, error) {
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return false, err
	}
	for _, r := range rules {
		if mark != 0 {
			if r.Mark == mark {
				return true, nil
			}
			continue
		}
		if r.Src != nil && r.Src.IP.Equal(ip) {
			return true, nil
		}
	}
	return false, nil
}

// Verify checks that the host will actually steer bypassed traffic out the
// bypass interface, and logs what it finds.
//
// This exists because the failure it detects is otherwise invisible: with no
// policy rule, Envoy still binds the source address, still connects, still
// reports success, and the traffic quietly leaves via the host default route.
// Nothing in Envoy's logs, stats or config dump reflects it -- the only symptom
// is that remote servers see the wrong address. Finding that once took a full
// debugging session, so the check runs at startup and says so plainly.
//
// Failures are warnings, never fatal: the proxy is still useful, and refusing
// to start over host configuration would be worse than steering nothing.
func Verify(ifName string, ip net.IP, probe string, mark int) {
	if probe == "" {
		probe = DefaultProbe
	}

	selector := fmt.Sprintf("from %s", ip)
	if mark != 0 {
		selector = fmt.Sprintf("fwmark %#x", mark)
	}

	found, err := selectorRuleExists(ip, mark)
	switch {
	case err != nil:
		logrus.Warnf("Bypass check: could not list ip rules: %s", err)
	case !found:
		logrus.Warnf("Bypass check: NO ip rule selects on '%s'. Bypassed traffic will follow the "+
			"host default route. Fix with 'ip rule add %s lookup <table> priority 150', "+
			"or run with -ip-rule to manage it here", selector, selector)
	}

	// In mark mode the lookup must NOT carry a source address. Passing one
	// would match any higher-priority source rule the host has (DSM installs
	// one at priority 3) and report a healthy bypass even when the fwmark rule
	// is missing or wrong.
	from := ip
	if mark != 0 {
		from = nil
	}
	bypassDev, err := routeGet(probe, from, mark)
	if err != nil {
		// iproute2 missing is not an error worth shouting about; the structural
		// check above already ran.
		logrus.Debugf("Bypass check: route lookup for '%s' unavailable: %s", selector, err)
		return
	}

	defaultDev, err := routeGet(probe, nil, 0)
	if err != nil {
		logrus.Debugf("Bypass check: default route lookup unavailable: %s", err)
		defaultDev = ""
	}

	switch {
	case bypassDev != ifName:
		logrus.Warnf("Bypass check: traffic matching '%s' to %s egresses %q, not the bypass interface %q. "+
			"The bypass is NOT working; check 'ip rule show' and the table it points at",
			selector, probe, bypassDev, ifName)
	case defaultDev == ifName:
		logrus.Infof("Bypass check: traffic matching '%s' egresses %s as expected, but so does "+
			"everything else -- there is no separate default path, so the split is currently a "+
			"no-op (is the VPN up?)", selector, ifName)
	default:
		logrus.Infof("Bypass check: OK. %s -> %s; everything else -> %s", selector, bypassDev, defaultDev)
	}
}
