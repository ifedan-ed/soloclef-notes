package main

import (
	"context"
	"strings"
	"testing"
)

func TestTheWorkerDoesNotStartWithoutADatabase(t *testing.T) {
	_, err := openDatabase(context.Background(), func(string) string { return "  " })
	if err == nil || !strings.Contains(err.Error(), EnvDatabaseURL) {
		t.Fatalf("with no address: %v", err)
	}
}

// The address may carry a password, and this error is logged.
func TestAnAddressThatCannotBeUsedIsNotQuoted(t *testing.T) {
	for _, address := range []string{
		"postgres://worker:s3cret-word@127.0.0.1:1/notes?sslmode=disable", // nothing listens on port 1
		"postgres://worker:s3cret-word@%zz/notes",                         // not a URL
	} {
		_, err := openDatabase(context.Background(), func(string) string { return address })
		if err == nil {
			t.Fatalf("%s was accepted", address)
		}
		if strings.Contains(err.Error(), "s3cret-word") {
			t.Errorf("the refusal quotes the password: %v", err)
		}
		if !strings.Contains(err.Error(), EnvDatabaseURL) {
			t.Errorf("the refusal does not name the setting: %v", err)
		}
	}
}
