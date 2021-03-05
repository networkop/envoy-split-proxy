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
			input:  []string{"asd.abc.com", "192.168.1.1"},
			output: nil,
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
