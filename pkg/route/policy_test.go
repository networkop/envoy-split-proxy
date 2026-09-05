package route

import (
	"net"
	"testing"

	"github.com/vishvananda/netlink"
)

func cidr(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func TestPickDefaultIgnoresOtherLinks(t *testing.T) {
	// With a tunnel up there is more than one default route; picking the wrong
	// one would send bypassed traffic straight back into the VPN.
	routes := []netlink.Route{
		{LinkIndex: 9, Dst: nil, Gw: net.ParseIP("10.31.196.1")}, // wg-pia
		{LinkIndex: 2, Dst: nil, Gw: net.ParseIP("172.16.0.1")},  // eth0
	}

	gw, ok := pickDefault(routes, 2)
	if !ok {
		t.Fatal("wanted a default route via link 2")
	}
	if !gw.Equal(net.ParseIP("172.16.0.1")) {
		t.Errorf("wanted gateway 172.16.0.1, got: %v", gw)
	}
}

func TestPickDefaultSkipsNonDefaultRoutes(t *testing.T) {
	routes := []netlink.Route{
		{LinkIndex: 2, Dst: cidr("172.16.0.0/24")},
		{LinkIndex: 2, Dst: cidr("158.173.3.61/32"), Gw: net.ParseIP("172.16.0.1")},
		{LinkIndex: 2, Dst: cidr("0.0.0.0/0"), Gw: net.ParseIP("172.16.0.1")},
	}

	gw, ok := pickDefault(routes, 2)
	if !ok || !gw.Equal(net.ParseIP("172.16.0.1")) {
		t.Errorf("wanted the 0.0.0.0/0 route's gateway, got: %v (%v)", gw, ok)
	}
}

func TestPickDefaultOnLink(t *testing.T) {
	// A default route with no gateway is valid and must be reported as found,
	// not confused with "no default route".
	gw, ok := pickDefault([]netlink.Route{{LinkIndex: 2, Dst: nil}}, 2)
	if !ok {
		t.Fatal("an on-link default route must count as found")
	}
	if gw != nil {
		t.Errorf("wanted a nil gateway, got: %v", gw)
	}
}

func TestPickDefaultAbsent(t *testing.T) {
	routes := []netlink.Route{{LinkIndex: 9, Dst: nil, Gw: net.ParseIP("10.31.196.1")}}
	if _, ok := pickDefault(routes, 2); ok {
		t.Error("wanted no default route via link 2")
	}
}

func TestRuleMatches(t *testing.T) {
	ip := net.ParseIP("172.16.0.90")
	mine := netlink.Rule{
		Priority: 150,
		Table:    200,
		Src:      &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)},
	}

	if !ruleMatches(mine, ip, 200, 150) {
		t.Error("wanted our own rule to match")
	}

	// Someone else's rule at the same priority must not be adopted or deleted.
	other := mine
	other.Table = 51820
	if ruleMatches(other, ip, 200, 150) {
		t.Error("a rule pointing at another table must not match")
	}

	// A rule for a different source is a leftover from an old address.
	stale := mine
	stale.Src = &net.IPNet{IP: net.ParseIP("172.16.0.91"), Mask: net.CIDRMask(32, 32)}
	if ruleMatches(stale, ip, 200, 150) {
		t.Error("a rule for a different source must not match")
	}

	// The VPN's own catch-all has no Src at all.
	catchAll := netlink.Rule{Priority: 150, Table: 200}
	if ruleMatches(catchAll, ip, 200, 150) {
		t.Error("a rule with no source must not match")
	}
}

func TestParseDev(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "bypassed, via the policy table",
			out:  "1.1.1.1 from 172.16.0.90 via 172.16.0.1 dev eth0  table 200 \n    cache",
			want: "eth0",
		},
		{
			name: "default path, into the tunnel",
			out:  "1.1.1.1 dev wg-pia  table 51820  src 10.31.196.44 \n    cache",
			want: "wg-pia",
		},
		{
			name: "on-link, no gateway",
			out:  "1.1.1.1 from 172.16.0.90 dev eth0 \n    cache",
			want: "eth0",
		},
		{
			name: "lookup failed",
			out:  "RTNETLINK answers: Network is unreachable",
			want: "",
		},
		{
			name: "trailing dev with nothing after it",
			out:  "1.1.1.1 dev",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseDev(tt.out); got != tt.want {
				t.Errorf("wanted %q, got: %q", tt.want, got)
			}
		})
	}
}
