// Package route manages the host policy routing that makes the bypass mean
// anything.
//
// Envoy binds bypassed upstream sockets to the bypass interface's address, but
// that only sets the source IP -- the kernel still picks the route. On a host
// running a full-tunnel VPN, the VPN's catch-all ip rule wins and its
// MASQUERADE rewrites the source to the tunnel address, so bypassed traffic
// succeeds while the far end sees the VPN's exit IP. Envoy's logs, stats and
// config dump all look healthy; the fault is invisible from inside the proxy.
//
// Owning the rule here rather than leaving it to the operator keeps the thing
// that binds the address and the thing that routes it in one place, and means
// the bypass survives a reboot without any host configuration.
package route

import (
	"fmt"
	"net"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

// mainTable is the kernel's main routing table (RT_TABLE_MAIN). Full-tunnel VPN
// agents conventionally leave its default route alone and steer traffic with a
// rule pointing at a table of their own, so it still describes the native path.
const mainTable = 254

var defaultNet = net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}

// Manager owns one ip rule and the default route in the table it points at.
type Manager struct {
	table    int
	priority int

	// applied is the address the installed rule currently matches, so a change
	// to the interface's address replaces the rule rather than leaving a stale
	// one behind alongside it.
	applied net.IP
}

// NewManager builds a Manager for the given routing table and rule priority.
//
// The priority has to sit below any "lookup main suppress_prefixlength 0" rule
// so LAN destinations still resolve from the main table and never reach this
// one, and above the VPN's catch-all so bypassed traffic beats the tunnel. 150
// works with the common 100/1000 layout.
func NewManager(table, priority int) *Manager {
	return &Manager{table: table, priority: priority}
}

// pickDefault returns the gateway of the default route leaving via linkIndex.
//
// Filtering on the link matters: with a tunnel up there is more than one
// default route in play, and picking the wrong one would send bypassed traffic
// straight back into it. A nil gateway means an on-link default, which is
// valid.
func pickDefault(routes []netlink.Route, linkIndex int) (net.IP, bool) {
	for _, r := range routes {
		if r.LinkIndex != linkIndex {
			continue
		}
		if r.Dst != nil && r.Dst.String() != defaultNet.String() {
			continue
		}
		return r.Gw, true
	}
	return nil, false
}

// ruleMatches reports whether r is the rule this Manager installs for ip.
func ruleMatches(r netlink.Rule, ip net.IP, table, priority int) bool {
	if r.Priority != priority || r.Table != table {
		return false
	}
	return r.Src != nil && r.Src.IP.Equal(ip)
}

func (m *Manager) rule(ip net.IP) *netlink.Rule {
	r := netlink.NewRule()
	r.Priority = m.priority
	r.Table = m.table
	r.Src = &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)}
	return r
}

// Ensure idempotently installs the default route and the rule for the given
// bypass interface and address. Safe to call on every config update.
func (m *Manager) Ensure(ifName string, ip net.IP) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("finding bypass interface %s: %w", ifName, err)
	}

	routes, err := netlink.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: mainTable},
		netlink.RT_FILTER_TABLE,
	)
	if err != nil {
		return fmt.Errorf("listing main table routes: %w", err)
	}

	gw, ok := pickDefault(routes, link.Attrs().Index)
	if !ok {
		return fmt.Errorf("no default route via %s in the main table", ifName)
	}

	route := &netlink.Route{
		Dst:       &defaultNet,
		Gw:        gw,
		LinkIndex: link.Attrs().Index,
		Table:     m.table,
	}
	if gw == nil {
		// An on-link default has to be link-scoped or the kernel rejects it.
		route.Scope = netlink.SCOPE_LINK
	}
	if err := netlink.RouteReplace(route); err != nil {
		return fmt.Errorf("installing default route in table %d: %w", m.table, err)
	}

	// Drop a rule left over from a previous address before adding the new one.
	if m.applied != nil && !m.applied.Equal(ip) {
		logrus.Infof("Bypass address changed from %s to %s, replacing ip rule", m.applied, ip)
		m.removeRule(m.applied)
	}

	present, err := m.ruleInstalled(ip)
	if err != nil {
		return err
	}
	if !present {
		if err := netlink.RuleAdd(m.rule(ip)); err != nil {
			return fmt.Errorf("installing ip rule from %s: %w", ip, err)
		}
		logrus.Infof("Installed ip rule: from %s lookup %d priority %d (via %s dev %s)",
			ip, m.table, m.priority, gw, ifName)
	}
	m.applied = ip

	return nil
}

func (m *Manager) ruleInstalled(ip net.IP) (bool, error) {
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return false, fmt.Errorf("listing ip rules: %w", err)
	}
	for _, r := range rules {
		if ruleMatches(r, ip, m.table, m.priority) {
			return true, nil
		}
	}
	return false, nil
}

func (m *Manager) removeRule(ip net.IP) {
	if err := netlink.RuleDel(m.rule(ip)); err != nil {
		logrus.Infof("Failed to remove ip rule from %s: %s", ip, err)
		return
	}
	logrus.Infof("Removed ip rule: from %s lookup %d priority %d", ip, m.table, m.priority)
}

// Remove deletes the rule and the table's default route. Errors are logged
// rather than returned: this runs on the shutdown path, where their absence is
// the desired end state anyway.
func (m *Manager) Remove() {
	if m.applied == nil {
		return
	}
	m.removeRule(m.applied)

	if err := netlink.RouteDel(&netlink.Route{Dst: &defaultNet, Table: m.table}); err != nil {
		logrus.Infof("Failed to remove default route from table %d: %s", m.table, err)
	}
	m.applied = nil
}
