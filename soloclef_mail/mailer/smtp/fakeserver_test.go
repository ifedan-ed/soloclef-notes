package smtp_test

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeServer is enough of an SMTP server to drive the session: it answers each
// command from a script, so a test can refuse exactly one step with exactly one
// code. A real server is what the Mailpit test is for; this is for the answers
// Mailpit will not give.
type fakeServer struct {
	listener net.Listener
	mu       sync.Mutex
	// refuse maps a command verb to the answer to give instead of the normal
	// one, e.g. "MAIL" to "550 not allowed".
	refuse map[string]string
	// commands is every line any connection sent.
	commands []string
	// connections counts the sessions opened, so a test can tell a kept one
	// from a reopened one.
	connections int
	extensions  []string
}

func startFakeServer(t *testing.T, refuse map[string]string, extensions []string) *fakeServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{listener: listener, refuse: refuse, extensions: extensions}
	go s.accept()
	t.Cleanup(func() { listener.Close() })
	return s
}

func (s *fakeServer) port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *fakeServer) accept() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.connections++
		s.mu.Unlock()
		go s.serve(conn)
	}
}

func (s *fakeServer) serve(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	write := func(format string, args ...any) {
		fmt.Fprintf(conn, format+"\r\n", args...)
	}
	write("220 fake.queue.test ESMTP")
	inData := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				if answer, ok := s.answerFor("DATA-END"); ok {
					write("%s", answer)
					continue
				}
				write("250 queued")
			}
			continue
		}
		s.mu.Lock()
		s.commands = append(s.commands, line)
		s.mu.Unlock()
		verb := strings.ToUpper(strings.Fields(line + " ")[0])
		if answer, ok := s.answerFor(verb); ok {
			write("%s", answer)
			continue
		}
		switch verb {
		case "EHLO":
			write("250-fake.queue.test")
			for i, extension := range s.extensions {
				if i == len(s.extensions)-1 {
					write("250 %s", extension)
				} else {
					write("250-%s", extension)
				}
			}
			if len(s.extensions) == 0 {
				write("250 SIZE 35882577")
			}
		case "MAIL", "RCPT", "RSET", "NOOP":
			write("250 ok")
		case "DATA":
			inData = true
			write("354 go ahead")
		case "QUIT":
			write("221 bye")
			return
		default:
			write("502 not implemented")
		}
	}
}

func (s *fakeServer) answerFor(verb string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	answer, ok := s.refuse[verb]
	return answer, ok
}

func (s *fakeServer) sessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connections
}

func (s *fakeServer) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}
