package uploads

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
)

// slowly is a body that arrives a piece at a time with a pause between
// pieces: a large file on a slow link, in miniature.
type slowly struct {
	pieces []string
	pause  time.Duration
}

func (s *slowly) Read(buffer []byte) (int, error) {
	if len(s.pieces) == 0 {
		return 0, io.EOF
	}
	time.Sleep(s.pause)
	read := copy(buffer, s.pieces[0])
	if read == len(s.pieces[0]) {
		s.pieces = s.pieces[1:]
	} else {
		s.pieces[0] = s.pieces[0][read:]
	}
	return read, nil
}

const boundary = "paced"

// formPieces is a one-file upload cut into its head, the file's content in
// `pieces` equal parts, and its tail.
func formPieces(content string, pieces int) []string {
	cut := []string{"--" + boundary + "\r\nContent-Disposition: form-data; name=\"parser\"\r\n\r\nlocal\r\n" +
		"--" + boundary + "\r\nContent-Disposition: form-data; name=\"files\"; filename=\"slow.pdf\"\r\n\r\n"}
	size := len(content) / pieces
	for index := 0; index < pieces; index++ {
		cut = append(cut, content[index*size:(index+1)*size])
	}
	return append(cut, "\r\n--"+boundary+"--\r\n")
}

// cmd/api's server cuts a request that takes longer than its ReadTimeout to
// arrive, and a response that starts later than its WriteTimeout. An upload
// routinely takes longer than both. The route moves both deadlines while
// bytes keep arriving, so an upload that outlasts the server's timeouts
// several times over still completes.
func TestAnUploadSlowerThanTheServersTimeoutsStillCompletes(t *testing.T) {
	const serverTimeout = 300 * time.Millisecond
	// The client may pause up to three seconds; it pauses 150 ms at a time.
	s := newSitePaced(t, parserchoice.Limits{MaxFileBytes: 1 << 20, MaxFiles: 1}, 3*time.Second, serverTimeout)

	content := strings.Repeat("0123456789", 100)
	// Ten pauses of 150 ms: 1.5 s in all, five times the server's timeout.
	body := &slowly{pieces: formPieces(content, 8), pause: 150 * time.Millisecond}
	started := time.Now()
	got := s.post(body, "multipart/form-data; boundary="+boundary)
	took := time.Since(started)

	if got.status != http.StatusOK {
		t.Fatalf("status %d after %s: %s", got.status, took, got.message)
	}
	if took < 3*serverTimeout {
		t.Fatalf("the upload took %s; it was meant to outlast the server's %s timeout", took, serverTimeout)
	}
	if held := s.titled("slow.pdf"); held == nil || held.originals[0].SHA256 != digest(content) {
		t.Errorf("the slow file was recorded as %+v", held)
	}
}

// The other half of the same rule: a client that stops sending is dropped
// after one stall timeout, however generous the route is with one that
// keeps going, and nothing of its file is kept.
func TestAClientThatStopsSendingIsDropped(t *testing.T) {
	s := newSitePaced(t, parserchoice.Limits{MaxFileBytes: 1 << 20, MaxFiles: 1}, 300*time.Millisecond, 0)

	pieces := formPieces(strings.Repeat("0123456789", 100), 2)
	// The head and half the file arrive at once; then nothing for far
	// longer than the stall timeout.
	request, err := http.NewRequest(http.MethodPost, s.server.URL+Path, io.MultiReader(
		strings.NewReader(pieces[0]+pieces[1]),
		&slowly{pieces: pieces[2:], pause: 3 * time.Second},
	))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

	started := time.Now()
	response, err := http.DefaultClient.Do(request)
	took := time.Since(started)
	if err == nil {
		defer response.Body.Close()
		// The route may get its refusal out before the connection closes;
		// either way it is not a success.
		if response.StatusCode == http.StatusOK {
			t.Fatalf("a stalled upload was accepted")
		}
	}
	if took > 2*time.Second {
		t.Errorf("the stalled client held its connection for %s; the stall timeout is 300ms", took)
	}

	// Nothing of the half-sent file is left once the route has let go.
	deadline := time.Now().Add(3 * time.Second)
	for len(s.stored()) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if left := s.stored(); len(left) != 0 {
		t.Errorf("%d objects left by a client that stopped sending", len(left))
	}
	if len(s.rows.documents) != 0 {
		t.Errorf("a stalled upload was recorded")
	}
}

func TestAFileIsCutOffAtTheLimitAndNotABytePast(t *testing.T) {
	for _, test := range []struct {
		size, limit int
		tooLarge    bool
	}{{size: 0, limit: 10}, {size: 9, limit: 10}, {size: 10, limit: 10}, {size: 11, limit: 10, tooLarge: true}, {size: 5000, limit: 10, tooLarge: true}} {
		reader := &capped{file: strings.NewReader(strings.Repeat("x", test.size)), limit: int64(test.limit)}
		passed, err := io.Copy(io.Discard, reader)
		if test.tooLarge {
			if err != errTooLarge {
				t.Errorf("%d bytes under a limit of %d: error %v", test.size, test.limit, err)
			}
			// What reached the store before the refusal is never more
			// than the limit: an oversized file cannot fill the disk.
			if passed > int64(test.limit) {
				t.Errorf("%d bytes were passed on under a limit of %d", passed, test.limit)
			}
			continue
		}
		if err != nil || passed != int64(test.size) {
			t.Errorf("%d bytes under a limit of %d: passed %d, %v", test.size, test.limit, passed, err)
		}
	}
}
