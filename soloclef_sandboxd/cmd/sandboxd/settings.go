package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// settings is the service's configuration. Every one is required: the
// Compose file writes each out (docker/docker-compose.go.yml, the sandbox
// service), and none has a default here, so a missing one stops the start
// and is named.
type settings struct {
	// Stack is the Compose project's name: on every sandbox's label and in
	// every lookup, so one stack never touches another's.
	Stack string
	// Image is the sandbox image (the stack's own build of docker/sandbox).
	Image string
	// Path is PATH for every command in a sandbox (a fact of the image).
	Path string
	// User is the numeric uid:gid commands run as (a fact of the image).
	User string
	// Listen is the address served, like ":8080".
	Listen string
	// DockerSocket is the daemon's socket.
	DockerSocket string
	// KeyFile holds the key callers must bear.
	KeyFile string
	// KeyCopy is where a copy of the key is published for the API and the
	// worker (publishKey says why).
	KeyCopy string
}

func load(getenv func(string) string) (settings, error) {
	var missing []string
	read := func(name string) string {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			missing = append(missing, name)
		}
		return value
	}
	s := settings{
		Stack:        read("SOLOCLEF_SANDBOXD_STACK"),
		Image:        read("SOLOCLEF_SANDBOXD_IMAGE"),
		Path:         read("SOLOCLEF_SANDBOXD_PATH"),
		User:         read("SOLOCLEF_SANDBOXD_USER"),
		Listen:       read("SOLOCLEF_SANDBOXD_LISTEN"),
		DockerSocket: read("SOLOCLEF_SANDBOXD_DOCKER_SOCKET"),
		KeyFile:      read("SOLOCLEF_SANDBOXD_KEY_FILE"),
		KeyCopy:      read("SOLOCLEF_SANDBOXD_KEY_COPY"),
	}
	if len(missing) > 0 {
		return settings{}, fmt.Errorf("sandboxd: not set: %s", strings.Join(missing, ", "))
	}
	if !strings.HasPrefix(s.Listen, ":") {
		return settings{}, fmt.Errorf("sandboxd: SOLOCLEF_SANDBOXD_LISTEN %q must be a port, like :8080", s.Listen)
	}
	return s, nil
}

// minimumKeyBytes refuses a key short enough to guess.
const minimumKeyBytes = 32

func readKey(file string) (string, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("sandboxd: reading the key: %w", err)
	}
	key := string(bytes.TrimSpace(raw))
	if len(key) < minimumKeyBytes {
		return "", errors.New("sandboxd: the key must be at least 32 characters (openssl rand -hex 32)")
	}
	return key, nil
}

// publishKey writes the key where the API and the worker read it: a volume
// they mount read-only. A copy, because Compose cannot mount a secret on a
// condition: mounted straight into them, the key file would have to exist on
// every deployment, the ones with the sandbox off too, while this service
// exists only when the sandbox is on. Written beside its final name and
// renamed over it, so a reader never sees half a key.
func publishKey(key, destination string) error {
	temporary := filepath.Join(filepath.Dir(destination), ".api_key.new")
	// A copy a crashed start left behind is read-only (0444), and this
	// process has no capability to write a file it may not write: it is
	// removed, never written into.
	if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sandboxd: publishing the key: %w", err)
	}
	if err := os.WriteFile(temporary, []byte(key+"\n"), 0o444); err != nil {
		return fmt.Errorf("sandboxd: publishing the key: %w", err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("sandboxd: publishing the key: %w", err)
	}
	return nil
}
