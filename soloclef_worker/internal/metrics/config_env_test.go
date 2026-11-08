package metrics

import "testing"

func TestLoadAddrServesByDefaultAndStopsOnlyWhenTold(t *testing.T) {
	cases := map[string]string{
		"":               DefaultAddr,
		"  ":             DefaultAddr,
		Off:              "",
		"127.0.0.1:9464": "127.0.0.1:9464",
		"  :9999  ":      ":9999",
	}
	for value, want := range cases {
		got := LoadAddr(func(string) string { return value })
		if got != want {
			t.Errorf("LoadAddr(%q) = %q, want %q", value, got, want)
		}
	}
}
