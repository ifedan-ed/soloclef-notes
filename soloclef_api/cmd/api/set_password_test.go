package main

import (
	"strings"
	"testing"
)

func TestThePasswordIsTheFirstLineOfStandardInput(t *testing.T) {
	for input, want := range map[string]string{
		"a passphrase\n":         "a passphrase",
		"a passphrase\r\nmore\n": "a passphrase",
		"no line end":            "no line end",
		" spaces kept \n":        " spaces kept ",
	} {
		if got, err := readPassword(strings.NewReader(input)); err != nil || got != want {
			t.Errorf("%q: %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"", "\n"} {
		if _, err := readPassword(strings.NewReader(input)); err == nil {
			t.Errorf("%q gave a password", input)
		}
	}
}
