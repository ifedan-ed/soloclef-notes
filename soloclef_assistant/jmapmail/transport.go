package jmapmail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// How much of a reply is read. A session resource is a few kilobytes; an
// API reply holds at most a page of summaries or one message whose text is
// itself bounded (maxBodyBytes); an attachment is bounded by its caller.
const (
	mostSessionBytes = 1 << 20
	mostReplyBytes   = 8 << 20
	// mostRedirects is how many same-origin redirects finding the session
	// may follow: Stalwart answers /.well-known/jmap with one.
	mostRedirects = 3
)

// Origin is a server address reduced to what identifies the server:
// scheme, host and port, no path, no credentials, no query. Every address
// the server hands back must have this same origin.
func Origin(address string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || parsed.Host == "" {
		return "", errors.New("jmapmail: the server's address is not a URL with a host")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("jmapmail: the server's address is not http(s)")
	}
	if parsed.User != nil {
		return "", errors.New("jmapmail: the server's address carries credentials")
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

// onOrigin resolves reference against base and refuses a result that is
// not on origin. Stalwart's session gives relative URLs ("/jmap/"); another
// server may give absolute ones; both are accepted, on the one origin.
func onOrigin(origin, base, reference string) (string, error) {
	from, err := url.Parse(base)
	if err != nil {
		return "", errBroken
	}
	to, err := url.Parse(reference)
	if err != nil {
		return "", errBroken
	}
	resolved := from.ResolveReference(to)
	if got, err := Origin(resolved.String()); err != nil || got != origin {
		return "", &Failure{Message: "The mail server pointed to an address on another server, which is not followed"}
	}
	return resolved.String(), nil
}

// exchange is one HTTP request to the server, signed in, its reply read up
// to most bytes. A transport error is the one unreachable sentence; a 401 or
// 403 is the server refusing the credential.
func (c Client) exchange(ctx context.Context, origin string, credential Credential, method, address string, body []byte, most int64) (int, http.Header, []byte, error) {
	var payload *requestBody
	if body != nil {
		payload = &requestBody{Reader: bytes.NewReader(body), Type: "application/json", Length: int64(len(body))}
	}
	return c.exchangeBody(ctx, origin, credential, method, address, payload, most)
}

// requestBody is what a request carries: a method call's JSON, or a file
// streamed to the upload endpoint without being held in memory.
type requestBody struct {
	Reader io.Reader
	// Type is the body's media type.
	Type string
	// Length is the body's exact length: sent as Content-Length, so the
	// server can refuse a file over its limit before reading it, and so a
	// stream that yields more or fewer bytes than said fails the request
	// instead of uploading something else.
	Length int64
}

// exchangeBody is exchange with any body (nil for none).
func (c Client) exchangeBody(ctx context.Context, origin string, credential Credential, method, address string, body *requestBody, most int64) (int, http.Header, []byte, error) {
	client, err := c.Network.Client(origin)
	if err != nil {
		return 0, nil, nil, errUnreachable
	}
	var reader io.Reader
	if body != nil {
		reader = body.Reader
	}
	request, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return 0, nil, nil, errUnreachable
	}
	credential.authorize(request)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", body.Type)
		request.ContentLength = body.Length
		if body.Length == 0 {
			request.Body = http.NoBody
		}
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, ctx.Err()
		}
		return 0, nil, nil, errUnreachable
	}
	defer response.Body.Close()
	// One byte more than allowed tells a reply that is over the limit from
	// one that is exactly at it.
	read, err := io.ReadAll(io.LimitReader(response.Body, most+1))
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, ctx.Err()
		}
		return 0, nil, nil, errBroken
	}
	if int64(len(read)) > most {
		return 0, nil, nil, &Failure{Message: fmt.Sprintf("The mail server's answer is larger than %d MB, which is more than is read", most>>20)}
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return response.StatusCode, response.Header, nil, errSignInRefused
	}
	return response.StatusCode, response.Header, read, nil
}
