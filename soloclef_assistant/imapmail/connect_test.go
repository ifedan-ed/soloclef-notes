package imapmail

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// mailServer is an IMAP server on loopback that knows one mailbox. It
// records every line it is sent.
type mailServer struct {
	listener net.Listener
	tls      *tls.Config
	// implicit is TLS from the first byte; otherwise STARTTLS.
	implicit bool

	// mu guards what a test and the connections' goroutines both touch: a
	// test changes the greeting between two connections while the first
	// connection's goroutine may still be running, and the race detector
	// knows no ordering between them that the network alone provides.
	mu sync.Mutex
	// greeting replaces the first line; set it with greet.
	greeting string
	lines    []string
	// folders are the messages the server holds, by folder's wire name
	// (mailbox_test.go).
	folders map[string][]held
}

// greet changes the first line the server sends to connections made from
// now on.
func (m *mailServer) greet(line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.greeting = line
}

// certificate is a test certificate valid for "example.com", and the pool
// that trusts it: the one net/http/httptest ships.
func certificate(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	lender := httptest.NewUnstartedServer(nil)
	lender.StartTLS()
	t.Cleanup(lender.Close)
	pool := x509.NewCertPool()
	pool.AddCert(lender.Certificate())
	return &tls.Config{Certificates: lender.TLS.Certificates}, pool
}

func newMailServer(t *testing.T, implicit bool) (*mailServer, *x509.CertPool) {
	t.Helper()
	config, pool := certificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &mailServer{listener: listener, tls: config, implicit: implicit, greeting: "* OK ready"}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	return server, pool
}

func (m *mailServer) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if m.implicit {
		conn = tls.Server(conn, m.tls)
	}
	reader := bufio.NewReader(conn)
	write := func(text string) { _, _ = conn.Write([]byte(text + "\r\n")) }
	m.mu.Lock()
	greeting := m.greeting
	m.mu.Unlock()
	write(greeting)
	selected := ""
	for {
		raw, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line := strings.TrimRight(raw, "\r\n")
		// A literal: say to go on, read its bytes, and carry on with the
		// rest of the command. It is recorded as a quoted word.
		for {
			size, follows := literalSize(line)
			if !follows {
				break
			}
			write("+ go on")
			sent := make([]byte, size)
			if _, err := io.ReadFull(reader, sent); err != nil {
				return
			}
			rest, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = line[:strings.LastIndexByte(line, '{')] + strconv.Quote(string(sent)) + strings.TrimRight(rest, "\r\n")
		}
		m.mu.Lock()
		m.lines = append(m.lines, line)
		m.mu.Unlock()
		tag, command, _ := strings.Cut(line, " ")
		switch {
		case command == "STARTTLS":
			write(tag + " OK begin TLS")
			conn = tls.Server(conn, m.tls)
			reader = bufio.NewReader(conn)
		case strings.HasPrefix(command, "LOGIN "):
			if command == `LOGIN "ada@example.com" "fake-pass \"quoted\" \\ word"` {
				write(tag + " OK signed in")
			} else {
				write(tag + " NO [AUTHENTICATIONFAILED] Invalid credentials\x07")
			}
		case strings.HasPrefix(command, "AUTHENTICATE XOAUTH2 "):
			response, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(command, "AUTHENTICATE XOAUTH2 "))
			if string(response) == "user=ada@example.com\x01auth=Bearer fake-access-token\x01\x01" {
				write(tag + " OK signed in")
				break
			}
			// As Exchange refuses a token: a challenge holding the reason,
			// which the client answers with an empty line.
			write("+ eyJzdGF0dXMiOiI0MDEifQ==")
			if reply, err := reader.ReadString('\n'); err != nil || strings.TrimRight(reply, "\r\n") != "" {
				return
			}
			write(tag + " NO AUTHENTICATE failed.")
		case strings.HasPrefix(command, "LIST "):
			write(`* LIST (\HasNoChildren) "/" "INBOX"`)
			write(`* LIST (\HasNoChildren) "/" "Sent"`)
			write(`* LIST (\HasNoChildren) "/" "Plans/2026"`)
			write(tag + " OK listed")
		case strings.HasPrefix(command, "EXAMINE "):
			name, _ := strconv.Unquote(strings.TrimPrefix(command, "EXAMINE "))
			if _, known := m.folders[name]; !known {
				write(tag + " NO no such folder")
				break
			}
			selected = name
			write(tag + " OK [READ-ONLY] opened")
		case strings.HasPrefix(command, "UID SEARCH "):
			uids := ""
			for _, message := range m.folders[selected] {
				if message.matches(criteriaWords(strings.TrimPrefix(command, "UID SEARCH "))) {
					uids += " " + strconv.Itoa(int(message.uid))
				}
			}
			write("* SEARCH" + uids)
			write(tag + " OK searched")
		case strings.HasPrefix(command, "UID FETCH "):
			set, items, _ := strings.Cut(strings.TrimPrefix(command, "UID FETCH "), " ")
			for sequence, message := range m.folders[selected] {
				if !slices.Contains(strings.Split(set, ","), strconv.Itoa(int(message.uid))) {
					continue
				}
				flags := ""
				if message.seen {
					flags = `\Seen`
				}
				prefix := "* " + strconv.Itoa(sequence+1) + " FETCH (UID " + strconv.Itoa(int(message.uid))
				if strings.Contains(items, "HEADER.FIELDS") {
					block := message.raw[:strings.Index(message.raw, "\r\n\r\n")+2]
					write(prefix + " FLAGS (" + flags + `) INTERNALDATE "` + message.received + `" BODY[HEADER.FIELDS (FROM TO SUBJECT)] {` + strconv.Itoa(len(block)) + "}")
					_, _ = conn.Write([]byte(block))
					write(")")
					continue
				}
				// A partial fetch, as BODY.PEEK[]<0.n> asks for one.
				body := message.raw
				if open := strings.Index(items, "<0."); open >= 0 {
					most, _ := strconv.Atoi(items[open+3 : strings.Index(items, ">")])
					body = body[:min(most, len(body))]
				}
				write(prefix + " RFC822.SIZE " + strconv.Itoa(len(message.raw)+message.unsent) + " BODY[]<0> {" + strconv.Itoa(len(body)) + "}")
				_, _ = conn.Write([]byte(body))
				write(")")
			}
			write(tag + " OK fetched")
		case command == "LOGOUT":
			write("* BYE")
			write(tag + " OK")
			return
		default:
			write(tag + " BAD unknown")
		}
	}
}

func (m *mailServer) sent() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.lines...)
}

// client is the real IMAP prober, with DNS answered from a table and the
// TCP connect sent to the test's server whatever address was pinned. The
// pinned address is recorded, which is what the guard's tests read.
func client(server *mailServer, pool *x509.CertPool, answers map[string][]string, dialled *[]string) IMAP {
	return IMAP{
		Timeout: 5 * time.Second, AttachmentTimeout: 10 * time.Second,
		Resolve: func(_ context.Context, host string) ([]netip.Addr, error) {
			found, known := answers[host]
			if !known {
				return nil, errors.New("no such host")
			}
			addresses := make([]netip.Addr, len(found))
			for index, text := range found {
				addresses[index] = netip.MustParseAddr(text)
			}
			return addresses, nil
		},
		dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			*dialled = append(*dialled, address)
			return (&net.Dialer{}).DialContext(ctx, network, server.listener.Addr().String())
		},
		roots: pool,
	}
}

func TestSigningInToAMailbox(t *testing.T) {
	answers := map[string][]string{"example.com": {"2606:2800:220:1::1", "93.184.216.34"}}
	good := Settings{Host: "example.com", Port: 993, UseTLS: true, Username: "ada@example.com", Password: `fake-pass "quoted" \ word`}

	for name, implicit := range map[string]bool{"TLS from the first byte": true, "STARTTLS": false} {
		t.Run(name, func(t *testing.T) {
			server, pool := newMailServer(t, implicit)
			var dialled []string
			mail := client(server, pool, answers, &dialled)
			settings := good
			settings.UseTLS = implicit
			folders, err := mail.Folders(context.Background(), settings)
			if err != nil || folders != 3 {
				t.Fatalf("Folders = %d, %v; want 3", folders, err)
			}
			// The IPv4 address, though the name answered IPv6 first, and
			// the port asked for.
			if len(dialled) != 1 || dialled[0] != "93.184.216.34:993" {
				t.Errorf("connected to %v", dialled)
			}
			sent := server.sent()
			if last := sent[len(sent)-1]; !strings.HasSuffix(last, " LOGOUT") {
				t.Errorf("the session was not ended: %q", sent)
			}

			settings.Password = "wrong"
			_, err = mail.Folders(context.Background(), settings)
			var refused *ConnectionError
			if !errors.As(err, &refused) || refused.Message != "The mail server refused the sign-in: NO [AUTHENTICATIONFAILED] Invalid credentials" {
				t.Errorf("a wrong password: %v", err)
			}
		})
	}

	t.Run("a mailbox with an access token signs in with XOAUTH2, never with LOGIN", func(t *testing.T) {
		server, pool := newMailServer(t, true)
		var dialled []string
		mail := client(server, pool, answers, &dialled)
		settings := Settings{Host: "example.com", Port: 993, UseTLS: true, Username: "ada@example.com", AccessToken: "fake-access-token"}
		folders, err := mail.Folders(context.Background(), settings)
		if err != nil || folders != 3 {
			t.Fatalf("Folders = %d, %v; want 3", folders, err)
		}
		settings.AccessToken = "fake-lapsed-token"
		_, err = mail.Folders(context.Background(), settings)
		var refused *ConnectionError
		if !errors.As(err, &refused) || refused.Message != "The mail server refused the sign-in: NO AUTHENTICATE failed." {
			t.Errorf("a refused token: %v", err)
		}
		for _, line := range server.sent() {
			if strings.Contains(line, " LOGIN ") {
				t.Errorf("a token mailbox sent %q", line)
			}
		}
		// A token that could end the sign-in's fields early is not sent.
		settings.AccessToken = "fake\x01user=someone-else"
		before := len(server.sent())
		if _, err = mail.Folders(context.Background(), settings); !errors.As(err, &refused) || len(server.sent()) != before {
			t.Errorf("a token holding a separator: %v", err)
		}
	})

	t.Run("nothing inside the network is connected to, and every such failure reads the same", func(t *testing.T) {
		server, pool := newMailServer(t, true)
		var dialled []string
		mail := client(server, pool, map[string][]string{
			"inside.example.com": {"10.0.0.5"},
			"mixed.example.com":  {"93.184.216.34", "127.0.0.1"},
			"mapped.example.com": {"::ffff:169.254.169.254"},
		}, &dialled)
		for _, host := range []string{"inside.example.com", "mixed.example.com", "mapped.example.com",
			"unknown.example.com", "127.0.0.1", "[::1]", "192.168.1.9", "redis"} {
			settings := good
			settings.Host = host
			_, err := mail.Folders(context.Background(), settings)
			var refused *ConnectionError
			if !errors.As(err, &refused) || refused.Message != unreachable {
				t.Errorf("%s: %v", host, err)
			}
			if mail.Reachable(context.Background(), host) {
				t.Errorf("%s may be saved", host)
			}
		}
		if len(dialled) != 0 {
			t.Errorf("connections were opened to %v", dialled)
		}
	})

	t.Run("a server that is not a mail server, or not the one named", func(t *testing.T) {
		server, pool := newMailServer(t, true)
		var dialled []string
		server.greet("HTTP/1.1 400 Bad Request")
		_, err := client(server, pool, answers, &dialled).Folders(context.Background(), good)
		var refused *ConnectionError
		if !errors.As(err, &refused) || refused.Message != unreachable {
			t.Errorf("not IMAP: %v", err)
		}
		// The certificate is for example.com: another name on the same
		// address is not trusted.
		server.greet("* OK ready")
		other := good
		other.Host = "elsewhere.example.org"
		_, err = client(server, pool, map[string][]string{"elsewhere.example.org": {"93.184.216.34"}}, &dialled).
			Folders(context.Background(), other)
		if !errors.As(err, &refused) || refused.Message != unreachable {
			t.Errorf("a certificate for another name: %v", err)
		}
		if len(server.sent()) != 0 {
			t.Errorf("a password was sent to an unproven server: %q", server.sent())
		}
	})

	t.Run("a password cannot carry a second command", func(t *testing.T) {
		server, pool := newMailServer(t, true)
		var dialled []string
		smuggled := good
		smuggled.Password = "x\"\r\na9 DELETE INBOX"
		if _, err := client(server, pool, answers, &dialled).Folders(context.Background(), smuggled); err == nil {
			t.Error("a password with a line break was sent")
		}
		if len(dialled) != 0 {
			t.Error("a connection was opened for it")
		}
	})
}
