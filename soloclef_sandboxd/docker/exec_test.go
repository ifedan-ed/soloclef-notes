package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// frame is one write of an exec's stream as the daemon frames it.
func frame(stream byte, payload string) []byte {
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	return append(header, payload...)
}

func TestTheStreamIsSplitIntoItsTwoOutputs(t *testing.T) {
	var stream bytes.Buffer
	stream.Write(frame(1, "out 1\n"))
	stream.Write(frame(2, "err\n"))
	stream.Write(frame(1, ""))
	stream.Write(frame(1, "out 2"))
	var stdout, stderr bytes.Buffer
	if err := demultiplex(&stream, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "out 1\nout 2" || stderr.String() != "err\n" {
		t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	// A frame cut short is an error, not a shorter output.
	cut := frame(1, "whole")
	if err := demultiplex(bytes.NewReader(cut[:10]), io.Discard, io.Discard); err == nil {
		t.Error("a cut frame was accepted")
	}
	// The daemon's own error, sent in the stream, is an error.
	if err := demultiplex(bytes.NewReader(frame(3, "exec failed")), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "exec failed") {
		t.Errorf("a system error frame: %v", err)
	}
}

// fakeDaemon answers the three requests of one exec on a Unix socket: its
// creation, its start (hijacking the connection: the process's input comes
// in, its output goes out), and its inspection.
func fakeDaemon(t *testing.T, exitCode int) (socket string, received *bytes.Buffer) {
	t.Helper()
	socket = filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	received = &bytes.Buffer{}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				for {
					request, err := http.ReadRequest(reader)
					if err != nil {
						return
					}
					switch {
					case strings.HasSuffix(request.URL.Path, "/exec") && request.Method == http.MethodPost:
						var body map[string]any
						_ = json.NewDecoder(request.Body).Decode(&body)
						if body["AttachStdin"] != true || body["User"] != "1000:1000" {
							t.Errorf("exec created with %v", body)
						}
						answer := `{"Id":"exec-1"}`
						io.WriteString(conn, "HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: "+strconv.Itoa(len(answer))+"\r\n\r\n"+answer)
					case strings.HasSuffix(request.URL.Path, "/start"):
						if request.Header.Get("Upgrade") != "tcp" {
							t.Errorf("start without the upgrade: %v", request.Header)
						}
						io.ReadAll(request.Body)
						io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.multiplexed-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
						// Echo the input once it is closed, as cat would.
						input, _ := io.ReadAll(reader)
						received.Write(input)
						conn.Write(frame(1, "got "+strconv.Itoa(len(input))+" bytes"))
						conn.Write(frame(2, "done"))
						return
					case strings.HasSuffix(request.URL.Path, "/json"):
						answer := `{"Running":false,"ExitCode":` + strconv.Itoa(exitCode) + `}`
						io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: "+strconv.Itoa(len(answer))+"\r\n\r\n"+answer)
					default:
						t.Errorf("unexpected %s %s", request.Method, request.URL.Path)
						return
					}
				}
			}()
		}
	}()
	return socket, received
}

func TestAnExecSendsItsInputAndReadsItsOutputAndExit(t *testing.T) {
	socket, received := fakeDaemon(t, 7)
	client := New(socket)
	var stdout, stderr bytes.Buffer
	code, err := client.Exec(context.Background(), "box", ExecOptions{
		Cmd: []string{"sh", "-c", "cat > /x"}, User: "1000:1000",
		Stdin: strings.NewReader("hello\x00world"), Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil || code != 7 {
		t.Fatalf("exit %d (%v)", code, err)
	}
	if received.String() != "hello\x00world" || stdout.String() != "got 11 bytes" || stderr.String() != "done" {
		t.Errorf("sent %q, stdout %q, stderr %q", received.String(), stdout.String(), stderr.String())
	}
}

func TestAnErrorFromTheDaemonCarriesItsStatus(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/"+apiVersion+"/") {
			t.Errorf("a request without the pinned version: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"message":"container box is not running"}`)
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	_, err = New(socket).Exec(context.Background(), "box", ExecOptions{Cmd: []string{"true"}, Stdout: io.Discard, Stderr: io.Discard})
	if !IsConflict(err) || !strings.Contains(err.Error(), "is not running") {
		t.Errorf("err = %v", err)
	}
	if err := New(socket).Remove(context.Background(), "box"); !IsConflict(err) {
		t.Errorf("remove: %v", err)
	}
}
