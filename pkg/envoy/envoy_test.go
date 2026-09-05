package envoy

import (
	"reflect"
	"testing"
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
