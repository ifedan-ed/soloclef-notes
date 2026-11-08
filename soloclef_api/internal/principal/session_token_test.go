package principal

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// Every token below was minted inside the running Python tier by
// fastapi_users.jwt.generate_jwt / PyJWT with the secret in testSecret, and
// each verdict is the verdict fastapi_users.jwt.decode_jwt gave for it there.
// Subject: 52fb3fe6-7da5-400b-8d36-5717af0b1b13, issued at 1762000000.
const (
	testSecret   = "golden-fixture-secret-not-a-real-key"
	testSubject  = "52fb3fe6-7da5-400b-8d36-5717af0b1b13"
	testIssuedAt = 1762000000

	tokenValid           = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsiZmFzdGFwaS11c2VyczphdXRoIl0sImlhdCI6MTc2MjAwMDAwMCwiZXhwIjoyMTA5NDQzODkzfQ.HHAnLeVRUFWfy6N06GxywXPfMp_Dkhd0g4qWBHwZiPU"
	tokenExpired         = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsiZmFzdGFwaS11c2VyczphdXRoIl0sImlhdCI6MTc2MjAwMDAwMCwiZXhwIjoxNzYyMDAwMDYwfQ.fxslZy0SxT72utJYk-DMHEvG1VY9tdvMTHAyDVx42B8"
	tokenWrongAudience   = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsic29tZW9uZS1lbHNlIl0sImlhdCI6MTc2MjAwMDAwMH0.amyEt7UcEmPIdFOEWjMrHSCsScWFChbKaKHC4WdNGPk"
	tokenNoAudience      = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJpYXQiOjE3NjIwMDAwMDB9.b7E8ajRcgf3SAtY2FWYlbP5TAzgNP1AAt_xtX3GzIn0"
	tokenOtherSecret     = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsiZmFzdGFwaS11c2VyczphdXRoIl0sImlhdCI6MTc2MjAwMDAwMH0.eRuOTyfgBfS1zOaR-C3vH69KU9v0V1QcoRZw0TzQyRA"
	tokenNoneAlgorithm   = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsiZmFzdGFwaS11c2VyczphdXRoIl0sImlhdCI6MTc2MjAwMDAwMH0."
	tokenForeignIssuer   = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsiZmFzdGFwaS11c2VyczphdXRoIl0sImlhdCI6MTc2MjAwMDAwMCwiaXNzIjoiaHR0cHM6Ly9zc28uZXhhbXBsZS5jb20vYXBwbGljYXRpb24vby9hcHAvIn0.v1g_TKIMTcxsWqKVAmxetjMJmAfEJchVIA97ZXW_7p4"
	tokenBeforeEpoch     = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsiZmFzdGFwaS11c2VyczphdXRoIl0sImlhdCI6MTc2MTkxMzYwMCwiZXhwIjoyMTA5NDQzODkzfQ.6fxUKkfRgkannj5GHC3yzvJZGLnB8NzuP2HkHV5fuVk"
	tokenBareUUIDSubject = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNjdkYTU0MDBiOGQzNjU3MTdhZjBiMWIxMyIsImF1ZCI6WyJmYXN0YXBpLXVzZXJzOmF1dGgiXSwiaWF0IjoxNzYyMDAwMDAwLCJleHAiOjIxMDk0NDM4OTN9.G01Oli722OrjK6haxd6Dafill1EPvOSk1i-Tr5TulZE"
)

func TestSessionTokenAcceptsWhatThePythonTierAccepts(t *testing.T) {
	reader := SessionToken{Secret: testSecret}

	for name, token := range map[string]string{
		"a token fastapi-users minted":      tokenValid,
		"a subject written without hyphens": tokenBareUUIDSubject,
	} {
		userID, err := reader.UserID(token)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if userID != testSubject {
			t.Errorf("%s: got %q, want %q", name, userID, testSubject)
		}
	}
}

func TestSessionTokenRefusesWhatThePythonTierRefuses(t *testing.T) {
	reader := SessionToken{Secret: testSecret}

	for name, token := range map[string]string{
		"expired":               tokenExpired,
		"another audience":      tokenWrongAudience,
		"no audience at all":    tokenNoAudience,
		"signed with other key": tokenOtherSecret,
		"unsigned (alg none)":   tokenNoneAlgorithm,
		"not a token":           "not-a-token",
		"empty":                 "",
	} {
		if _, err := reader.UserID(token); err == nil {
			t.Errorf("%s: accepted, want refused", name)
		}
	}
}

func TestSessionTokenRefusesTokensIssuedBeforeTheEpoch(t *testing.T) {
	// app.users._token_meets_epoch: MIN_ISSUED_AT ends every session minted
	// before it, and 0 switches the check off.
	withEpoch := SessionToken{Secret: testSecret, MinIssuedAt: testIssuedAt}
	if _, err := withEpoch.UserID(tokenBeforeEpoch); err == nil {
		t.Error("a token issued before the epoch was accepted")
	}
	if _, err := withEpoch.UserID(tokenValid); err != nil {
		t.Errorf("a token issued at the epoch was refused: %v", err)
	}
	withoutEpoch := SessionToken{Secret: testSecret}
	if _, err := withoutEpoch.UserID(tokenBeforeEpoch); err != nil {
		t.Errorf("with no epoch set the token must be accepted: %v", err)
	}
}

func TestSessionTokenSendsAForeignIssuerElsewhere(t *testing.T) {
	// Verified or not, a token naming an issuer is not one of this
	// application's: the Python tier's own tokens carry no iss claim. It must
	// be reported as somebody else's rather than refused here, so the request
	// can go to the tier that can check it.
	_, err := SessionToken{Secret: testSecret}.UserID(tokenForeignIssuer)
	if !errors.Is(err, ErrForeignIssuer) {
		t.Errorf("got %v, want ErrForeignIssuer", err)
	}
}

// Which tier can verify a token is decided by reading its issuer, and that read
// has to agree with PyJWT's: a token it cannot read is not an agent's, and takes
// the session path to the same 401. Each of these was found by asking both tiers
// and comparing the bytes, not by reading the two libraries.
func TestReadingTheIssuerAgreesWithPyJWTsOwnRead(t *testing.T) {
	cases := map[string]struct {
		token  string
		issuer string
	}{
		"an identity provider's token": {tokenForeignIssuer, "https://sso.example.com/application/o/app/"},
		"this application's own token": {tokenValid, ""},
		"a header with no alg at all":  {withHeader(t, tokenForeignIssuer, `{"typ":"JWT"}`), "https://sso.example.com/application/o/app/"},
		"a kid that is not a string":   {withHeader(t, tokenForeignIssuer, `{"alg":"RS256","kid":7}`), ""},
		"a kid that is a string":       {withHeader(t, tokenForeignIssuer, `{"alg":"RS256","kid":"a"}`), "https://sso.example.com/application/o/app/"},
		// PyJWT discards the junk and reads the token, so this tier does too:
		// the signature is then checked properly, and refused, on the agent path.
		"a signature padded out with junk":  {signatureOf(t, tokenForeignIssuer, "QUJD!!!"), "https://sso.example.com/application/o/app/"},
		"a signature binascii will not pad": {signatureOf(t, tokenForeignIssuer, "QUJDRA!!"), ""},
		"two segments":                      {"abc.def", ""},
		"nothing that looks like a token":   {"nonsense", ""},
	}
	for name, test := range cases {
		issuer, found := unverifiedIssuer(test.token)
		if test.issuer == "" {
			if found {
				t.Errorf("%s: read %q, want nothing", name, issuer)
			}
			continue
		}
		if !found || issuer != test.issuer {
			t.Errorf("%s: read %q/%v, want %q", name, issuer, found, test.issuer)
		}
	}
}

func withHeader(t *testing.T, token, header string) string {
	t.Helper()
	segments := strings.Split(token, ".")
	segments[0] = base64.RawURLEncoding.EncodeToString([]byte(header))
	return strings.Join(segments, ".")
}

func signatureOf(t *testing.T, token, signature string) string {
	t.Helper()
	segments := strings.Split(token, ".")
	segments[2] = signature
	return strings.Join(segments, ".")
}
