package main

import (
	"errors"
	"strings"
	"testing"
)

func environment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestTheDatabaseURLTakesItsPasswordOnce(t *testing.T) {
	secret := func(content string) func(string) ([]byte, error) {
		return func(path string) ([]byte, error) {
			if path != "/run/secrets/postgres_password" {
				return nil, errors.New("no such file")
			}
			return []byte(content), nil
		}
	}
	cases := []struct {
		name    string
		env     map[string]string
		file    string
		want    string
		refusal string
	}{
		{name: "the URL as given when no file is named",
			env:  map[string]string{DatabaseURL: "postgres://app:inline@db:5432/notes"},
			want: "postgres://app:inline@db:5432/notes"},
		{name: "the password from its file, without the trailing newline",
			env:  map[string]string{DatabaseURL: "postgres://app@db:5432/notes?sslmode=disable", DatabasePasswordFile: "/run/secrets/postgres_password"},
			file: "p@ss/word\n",
			want: "postgres://app:p%40ss%2Fword@db:5432/notes?sslmode=disable"},
		{name: "no address", env: map[string]string{}, refusal: DatabaseURL + " is not set"},
		{name: "a password given twice",
			env:     map[string]string{DatabaseURL: "postgres://app:inline@db/notes", DatabasePasswordFile: "/run/secrets/postgres_password"},
			file:    "other",
			refusal: "give the password once"},
		{name: "a file and a URL with no user",
			env:     map[string]string{DatabaseURL: "postgres://db/notes", DatabasePasswordFile: "/run/secrets/postgres_password"},
			file:    "secret",
			refusal: "must be a URL that names a user"},
		{name: "a file that cannot be read",
			env:     map[string]string{DatabaseURL: "postgres://app@db/notes", DatabasePasswordFile: "/elsewhere"},
			refusal: "no such file"},
		{name: "an empty file",
			env:     map[string]string{DatabaseURL: "postgres://app@db/notes", DatabasePasswordFile: "/run/secrets/postgres_password"},
			file:    "\n",
			refusal: "is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := databaseURL(environment(c.env), secret(c.file))
			if c.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), c.refusal) {
					t.Fatalf("got %q, %v; want a refusal saying %q", got, err, c.refusal)
				}
				// A refusal is logged, and the URL may hold a password.
				if strings.Contains(err.Error(), "inline") {
					t.Errorf("the refusal quotes the password: %v", err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestDownIsAllowedOnlyForTheDatabaseNamed(t *testing.T) {
	if err := allowDown(environment(map[string]string{DevelopmentDatabase: "notes_dev"}), "notes_dev"); err != nil {
		t.Errorf("the named database was refused: %v", err)
	}
	for name, env := range map[string]map[string]string{
		"nothing set":      {},
		"a blanket yes":    {DevelopmentDatabase: "true"},
		"another database": {DevelopmentDatabase: "notes_test"},
	} {
		if err := allowDown(environment(env), "notes_dev"); err == nil {
			t.Errorf("%s: down-to was allowed", name)
		}
	}
}
