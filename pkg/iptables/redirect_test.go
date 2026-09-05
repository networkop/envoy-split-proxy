package iptables

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fakeRun records invocations and pretends the given rules already exist.
type fakeRun struct {
	calls   [][]string
	present map[string]bool
}

func (f *fakeRun) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if args[2] == "-C" {
		if f.present[strings.Join(args, " ")] {
			return nil, nil
		}
		return nil, fmt.Errorf("exit status 1")
	}
	return nil, nil
}

func (f *fakeRun) actions() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c[2])
	}
	return out
}

func newTestManager(f *fakeRun, rules ...Rule) *Manager {
	return &Manager{rules: rules, run: f.run}
}

func TestRuleArgs(t *testing.T) {
	want := []string{
		"-t", "nat", "-A", "PREROUTING", "-p", "tcp",
		"--dport", "443", "-j", "REDIRECT", "--to-port", "10000",
	}
	if got := (Rule{DestPort: 443, ToPort: 10000}).args("-A"); !reflect.DeepEqual(got, want) {
		t.Errorf("wanted %v, got: %v", want, got)
	}
}

func TestEnsureAddsMissingRules(t *testing.T) {
	f := &fakeRun{present: map[string]bool{}}
	m := newTestManager(f, Rule{DestPort: 443, ToPort: 10000}, Rule{DestPort: 80, ToPort: 10001})

	if err := m.Ensure(); err != nil {
		t.Fatalf("Ensure: %s", err)
	}

	// each rule: one -C probe followed by one -A
	want := []string{"-C", "-A", "-C", "-A"}
	if got := f.actions(); !reflect.DeepEqual(got, want) {
		t.Errorf("wanted actions %v, got: %v", want, got)
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	rule := Rule{DestPort: 443, ToPort: 10000}
	f := &fakeRun{present: map[string]bool{
		strings.Join(rule.args("-C"), " "): true,
	}}
	m := newTestManager(f, rule)

	if err := m.Ensure(); err != nil {
		t.Fatalf("Ensure: %s", err)
	}

	// an already-present rule must be probed and left alone, never re-added
	if got := f.actions(); !reflect.DeepEqual(got, []string{"-C"}) {
		t.Errorf("wanted a probe only, got: %v", got)
	}
}

func TestRemoveSkipsAbsentRules(t *testing.T) {
	present := Rule{DestPort: 443, ToPort: 10000}
	absent := Rule{DestPort: 80, ToPort: 10001}
	f := &fakeRun{present: map[string]bool{
		strings.Join(present.args("-C"), " "): true,
	}}
	m := newTestManager(f, present, absent)

	m.Remove()

	want := []string{"-C", "-D", "-C"}
	if got := f.actions(); !reflect.DeepEqual(got, want) {
		t.Errorf("wanted actions %v, got: %v", want, got)
	}
}
