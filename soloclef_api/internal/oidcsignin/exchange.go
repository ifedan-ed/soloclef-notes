package oidcsignin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// exchangeTimeout bounds the one call this tier makes to the provider on a
// person's behalf while their browser waits.
const exchangeTimeout = 10 * time.Second

// maxTokenResponse caps what is read from the token endpoint. A real answer
// is a few kilobytes.
const maxTokenResponse = 1 << 20

// defaultHTTPClient follows no redirect: the form carries the client secret
// and the code, and neither is to be re-posted wherever a 307 points.
var defaultHTTPClient = &http.Client{
	Timeout: exchangeTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// exchangeCode trades the callback's code for the provider's ID token.
//
// The provider's access and refresh tokens come back in the same answer and
// are dropped: this tier never calls the provider again for the person, so
// it has no use for them and stores neither.
func exchangeCode(ctx context.Context, client *http.Client, endpoint string, settings Settings, verifier, code string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {settings.RedirectURL},
		"code_verifier": {verifier},
	}
	ctx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", refused(codeExchangeFailed, "the token endpoint is not an address: "+err.Error())
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	// client_secret_basic, the default every provider accepts. The two are
	// form-encoded first, as RFC 6749 2.3.1 says, so a secret with a colon
	// or a percent sign in it is still the secret the provider reads.
	request.SetBasicAuth(url.QueryEscape(settings.ClientID), url.QueryEscape(settings.ClientSecret))

	response, err := client.Do(request)
	if err != nil {
		return "", refused(codeExchangeFailed, "the token endpoint could not be reached: "+err.Error())
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTokenResponse))
	if err != nil {
		return "", refused(codeExchangeFailed, "the token endpoint's answer could not be read: "+err.Error())
	}
	if response.StatusCode != http.StatusOK {
		// The body of a refusal names the OAuth error (invalid_grant and
		// the like) and never carries a token, so it is safe to log.
		return "", refused(codeExchangeFailed, fmt.Sprintf("the token endpoint answered %d %.300s", response.StatusCode, body))
	}
	var answer struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.IDToken == "" {
		return "", refused(codeExchangeFailed, "the token endpoint sent no ID token")
	}
	return answer.IDToken, nil
}
