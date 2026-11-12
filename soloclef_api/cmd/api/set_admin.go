package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/config"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signin"
)

// setAdminCommand makes an account an administrator, or an ordinary account
// again:
//
//	docker exec <api container> soloclef-api set-admin person@example.org true
//
// It is a command for the reason set-password is one: who administers a
// deployment is its operator's to say, and the image has no shell or SQL
// client to say it with. It replaces the list of addresses in a setting
// (BILLING_ADMIN_EMAILS): the account's own `is_superuser` is now the only
// thing an admin check reads.
const setAdminCommand = "set-admin"

// administratorFlag reads the command's second argument: exactly "true" or
// "false", as every switch of this tier is read.
func administratorFlag(text string) (bool, error) {
	switch text {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%q is neither true nor false", text)
}

// setAdmin runs the command against the database this deployment names.
func setAdmin(address, flag string, output io.Writer) error {
	administrator, err := administratorFlag(flag)
	if err != nil {
		return err
	}
	settings, err := config.FromEnvironment()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, settings.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()
	userID, err := signin.SetAdministrator(ctx, pool, address, administrator)
	if err != nil {
		return err
	}
	what := "an ordinary account"
	if administrator {
		what = "an administrator"
	}
	_, err = fmt.Fprintf(output, "the account %s (%s) is now %s\n", strings.TrimSpace(address), userID, what)
	return err
}
