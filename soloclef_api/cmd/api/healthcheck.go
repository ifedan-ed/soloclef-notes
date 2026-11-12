package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/health"
)

// healthcheckCommand is the one argument this binary takes: with it, the
// process is not the server but a question put to the server.
//
// It exists because the image this binary ships in has nothing else in it:
// no shell, no curl, no wget (the Dockerfile says why). A container
// healthcheck has to run *something* inside the container, so the binary
// asks itself. The alternative, adding a probing tool to the image, would be
// a second program to keep patched for the sake of one GET.
const healthcheckCommand = "healthcheck"

// healthcheckTimeout bounds the whole probe. It is shorter than any sensible
// healthcheck timeout in a Compose file or an orchestrator, so that the
// probe reports "did not answer" itself instead of being killed mid-wait
// and leaving nothing in the health log.
const healthcheckTimeout = 3 * time.Second

// healthcheck asks the server listening in this container whether it can
// serve, and returns nil only for a 200 from health.Path.
//
// It reads one setting, the address the server was told to listen on, and
// deliberately not the whole configuration: loading that opens the secret
// files, and a probe that runs every few seconds has no business reading a
// credential it does not use.
func healthcheck(env func(string) string) error {
	const name = "SOLOCLEF_API_LISTEN_ADDRESS"
	address, err := probeAddress(strings.TrimSpace(env(name)))
	if err != nil {
		return fmt.Errorf("%s %w", name, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+health.Path, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("the server at %s did not answer %s: %w", address, health.Path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// 503 is the route's own word for "the database is unreachable".
		return fmt.Errorf("the server at %s answered %s with %d", address, health.Path, response.StatusCode)
	}
	return nil
}

// probeAddress is the address to dial for a server told to listen on
// `listen`. A server bound to every interface (":8080", "0.0.0.0:8080",
// "[::]:8080") is asked on loopback, since "every interface" is not an
// address anyone can dial; a server bound to one address is asked on that
// address, because it is not listening on any other.
func probeAddress(listen string) (string, error) {
	if listen == "" {
		return "", fmt.Errorf("is required")
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("is %q, which is not host:port: %w", listen, err)
	}
	if host == "" {
		host = "127.0.0.1"
	} else if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
		if ip.To4() == nil {
			host = "::1"
		}
	}
	return net.JoinHostPort(host, port), nil
}
