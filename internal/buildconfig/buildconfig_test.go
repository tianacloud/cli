package buildconfig

import "testing"

func TestTLSInsecureParsing(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "false", want: false},
		{value: "true", want: true},
		{value: "TRUE", want: true},
		{value: " true ", want: true},
		{value: "", want: false},
		{value: "yes", want: false},
	}
	previous := InsecureTLS
	defer func() { InsecureTLS = previous }()
	for _, test := range tests {
		InsecureTLS = test.value
		if got := TLSInsecure(); got != test.want {
			t.Fatalf("InsecureTLS=%q TLSInsecure()=%v, want %v", test.value, got, test.want)
		}
	}
}
