package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/config"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signin"
)

// setPasswordCommand makes an account, or gives an existing one a new
// password:
//
//	printf '%s\n' "$PASSWORD" | docker exec -i <api container> soloclef-api set-password person@example.org
//
// It is a command of this binary, not a route, because there is no open
// sign-up: accounts are the operator's to make, and the image has no shell
// or other tool to make one with. The password is read from standard input,
// never from an argument, so it is not left in a process list or a shell
// history.
const setPasswordCommand = "set-password"

// readPassword is the first line of standard input, without its line end.
func readPassword(input io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(input, 4096)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return "", errors.New("no password on standard input")
	}
	return password, nil
}

// setPassword runs the command against the database this deployment names.
func setPassword(address string, input io.Reader, output io.Writer) error {
	password, err := readPassword(input)
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
	userID, created, err := signin.SetPassword(ctx, pool, signin.NewHasher(1), address, password, time.Now().UTC())
	if err != nil {
		return err
	}
	verb := "set a new password for"
	if created {
		verb = "made"
	}
	_, err = fmt.Fprintf(output, "%s the account %s (%s); every earlier sign-in of it is ended\n", verb, strings.TrimSpace(address), userID)
	return err
}
