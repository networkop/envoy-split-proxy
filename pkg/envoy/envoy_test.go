package envoy

import (
	"reflect"
	"strings"
	"testing"

	api "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
)

func TestFilterPartialWildcard(t *testing.T) {

	tests := []struct {
		input  []string
		output []string
	}{
		{
			input:  []string{"*.abc.com", "*.1.1.1"},
			output: []string{"*.abc.com"},
		},
		{
			input:  []string{"*.abc.com", "192.168.1.1"},
			output: []string{"*.abc.com"},
		},
		{
			// exact hostnames are valid server_names and must be kept
			input:  []string{"asd.abc.com", "192.168.1.1"},
			output: []string{"asd.abc.com"},
		},
		{
			input:  []string{"nflximg.com", "netflix.net", "81.130.98.*"},
			output: []string{"nflximg.com", "netflix.net"},
		},
		{
			input:  []string{"*-bar.foo.com", "*.foo.com"},
			output: []string{"*.foo.com"},
		},
		{
			input:  []string{"*.abc.com", "*"},
			output: []string{"*.abc.com"},
		},
		{
			input:  []string{"*.abc.com", "*.asd.1.1"},
			output: []string{"*.abc.com", "*.asd.1.1"},
		},
	}
	for i, tt := range tests {
		t.Run("FilterPartialWildcard", func(t *testing.T) {
			output := excludePartialWildCards(tt.input)
			if !reflect.DeepEqual(output, tt.output) {
				t.Errorf("#%d wanted %v, got: %v", i, tt.output, output)
			}
		})
	}
}

func TestWithDefaultPort(t *testing.T) {
	input := []string{"*.netflix.com", "81.130.98.*"}
	want := []string{"*.netflix.com", "*.netflix.com:80", "81.130.98.*", "81.130.98.*:80"}

	got := withDefaultPort(input)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wanted %v, got: %v", want, got)
	}
}

func TestBypassBindConfig(t *testing.T) {
	withMark := newBypassBindConfig("172.16.0.90", 0x51821)
	opts := withMark.GetSocketOptions()
	if len(opts) != 1 {
		t.Fatalf("wanted 1 socket option, got: %d", len(opts))
	}
	if opts[0].GetIntValue() != 0x51821 {
		t.Errorf("wanted mark 0x51821, got: %#x", opts[0].GetIntValue())
	}
	if opts[0].GetState() != core.SocketOption_STATE_PREBIND {
		t.Errorf("SO_MARK must be applied pre-bind, got state: %v", opts[0].GetState())
	}

	// a zero mark leaves the socket untouched, so no CAP_NET_ADMIN is needed
	if opts := newBypassBindConfig("172.16.0.90", 0).GetSocketOptions(); len(opts) != 0 {
		t.Errorf("wanted no socket options when mark is 0, got: %d", len(opts))
	}
}

func TestBuildClusterOnlyMarksBypass(t *testing.T) {
	for _, r := range buildCluster("172.16.0.90", 0x51821) {
		c := r.(*api.Cluster)
		bind := c.GetUpstreamBindConfig()
		if strings.Contains(c.GetName(), "-bypass-") {
			if bind == nil || len(bind.GetSocketOptions()) != 1 {
				t.Errorf("%s: bypass cluster must carry the fwmark", c.GetName())
			}
			continue
		}
		if bind != nil {
			t.Errorf("%s: default cluster must not bind or mark its sockets", c.GetName())
		}
	}
}

func TestBypassBindUnboundWhenMarked(t *testing.T) {
	// A host may already have a higher-priority source rule for the interface
	// address, so with a mark configured the source must be left unspecified or
	// that rule decides the route instead of the mark.
	marked := newBypassBindConfig("172.16.0.90", 0x51821)
	if got := marked.GetSourceAddress().GetAddress(); got != "0.0.0.0" {
		t.Errorf("wanted an unbound source with a mark set, got: %v", got)
	}
	if len(marked.GetSocketOptions()) != 1 {
		t.Error("wanted the fwmark socket option to still be set")
	}

	unmarked := newBypassBindConfig("172.16.0.90", 0)
	if got := unmarked.GetSourceAddress().GetAddress(); got != "172.16.0.90" {
		t.Errorf("without a mark the source address is the selector, got: %v", got)
	}
}
