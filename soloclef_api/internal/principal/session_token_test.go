package principal

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// The refusals below were minted inside the running Python tier by
// fastapi_users.jwt.generate_jwt / PyJWT with the secret in testSecret, and
// each was refused by fastapi_users.jwt.decode_jwt there too.
// Subject: 52fb3fe6-7da5-400b-8d36-5717af0b1b13, issued at 1762000000.
//
// tokenWithoutSignIn is the one the Python tier *accepted*: it names the
// account and no sign-in. This tier refuses it since 2026-11-12, because
// nothing could end such a token before its expiry. The tokens this tier
// accepts are minted by signedToken below, as internal/signin mints them.
const (
	testSecret   = "golden-fixture-secret-not-a-real-key"
	testSubject  = "52fb3fe6-7da5-400b-8d36-5717af0b1b13"
	testSignIn   = "0b9a6f3e-5d2c-4e8f-9a1b-2c3d4e5f6a7b"
	testIssuedAt = 1762000000

	tokenWithoutSignIn = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsiZmFzdGFwaS11c2VyczphdXRoIl0sImlhdCI6MTc2MjAwMDAwMCwiZXhwIjoyMTA5NDQzODkzfQ.HHAnLeVRUFWfy6N06GxywXPfMp_Dkhd0g4qWBHwZiPU"
	tokenExpired       = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsiZmFzdGFwaS11c2VyczphdXRoIl0sImlhdCI6MTc2MjAwMDAwMCwiZXhwIjoxNzYyMDAwMDYwfQ.fxslZy0SxT72utJYk-DMHEvG1VY9tdvMTHAyDVx42B8"
	tokenWrongAudience = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsic29tZW9uZS1lbHNlIl0sImlhdCI6MTc2MjAwMDAwMH0.amyEt7UcEmPIdFOEWjMrHSCsScWFChbKaKHC4WdNGPk"
	tokenNoAudience    = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJpYXQiOjE3NjIwMDAwMDB9.b7E8ajRcgf3SAtY2FWYlbP5TAzgNP1AAt_xtX3GzIn0"
	tokenOtherSecret   = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsiZmFzdGFwaS11c2VyczphdXRoIl0sImlhdCI6MTc2MjAwMDAwMH0.eRuOTyfgBfS1zOaR-C3vH69KU9v0V1QcoRZw0TzQyRA"
	tokenNoneAlgorithm = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsiZmFzdGFwaS11c2VyczphdXRoIl0sImlhdCI6MTc2MjAwMDAwMH0."
	tokenForeignIssuer = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI1MmZiM2ZlNi03ZGE1LTQwMGItOGQzNi01NzE3YWYwYjFiMTMiLCJhdWQiOlsiZmFzdGFwaS11c2VyczphdXRoIl0sImlhdCI6MTc2MjAwMDAwMCwiaXNzIjoiaHR0cHM6Ly9zc28uZXhhbXBsZS5jb20vYXBwbGljYXRpb24vby9hcHAvIn0.v1g_TKIMTcxsWqKVAmxetjMJmAfEJchVIA97ZXW_7p4"
)

// signedToken is an access token as internal/signin mints one, with claims
// changed or removed (a nil value removes the claim).
func signedToken(secret string, changes jwt.MapClaims) string {
	claims := jwt.MapClaims{
		"sub": testSubject,
		"sid": testSignIn,
		"aud": tokenAudience,
		"iat": int64(testIssuedAt),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	for name, value := range changes {
		if value == nil {
			delete(claims, name)
			continue
		}
		claims[name] = value
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		panic(err)
	}
	return signed
}

// tokenValid is a token of a live sign-in of testSubject.
var tokenValid = signedToken(testSecret, nil)

func TestSessionTokenReadsTheAccountAndTheSignIn(t *testing.T) {
	reader := SessionToken{Secret: testSecret}

	for name, token := range map[string]string{
		"a token a sign-in minted":                 tokenValid,
		"a subject written without hyphens":        signedToken(testSecret, jwt.MapClaims{"sub": strings.ReplaceAll(testSubject, "-", "")}),
		"a sign-in id written in capitals, braced": signedToken(testSecret, jwt.MapClaims{"sid": "{" + strings.ToUpper(testSignIn) + "}"}),
	} {
		session, err := reader.Read(token)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if session.UserID != testSubject || session.SessionID != testSignIn {
			t.Errorf("%s: got %+v, want %s in sign-in %s", name, session, testSubject, testSignIn)
		}
	}
}

func TestSessionTokenRefusesATokenThatCannotBeEndedOrIsNotGood(t *testing.T) {
	reader := SessionToken{Secret: testSecret}

	for name, token := range map[string]string{
		"no sign-in (the Python tier's, or one from before 2026-11-12)": tokenWithoutSignIn,
		"no sign-in, minted here":                 signedToken(testSecret, jwt.MapClaims{"sid": nil}),
		"a sign-in that is not an id":             signedToken(testSecret, jwt.MapClaims{"sid": "not-a-sign-in"}),
		"a sign-in that is not text":              signedToken(testSecret, jwt.MapClaims{"sid": 42}),
		"expired":                                 tokenExpired,
		"another audience":                        tokenWrongAudience,
		"no audience at all":                      tokenNoAudience,
		"signed with other key":                   tokenOtherSecret,
		"signed with other key, naming a sign-in": signedToken("another-secret", nil),
		"unsigned (alg none)":                     tokenNoneAlgorithm,
		"not a token":                             "not-a-token",
		"empty":                                   "",
	} {
		if session, err := reader.Read(token); err == nil {
			t.Errorf("%s: accepted as %+v, want refused", name, session)
		}
	}
}

func TestSessionTokenRefusesTokensIssuedBeforeTheEpoch(t *testing.T) {
	// MIN_ISSUED_AT ends every session minted before it, and 0 switches the
	// check off.
	beforeEpoch := signedToken(testSecret, jwt.MapClaims{"iat": int64(testIssuedAt - 86400)})
	withEpoch := SessionToken{Secret: testSecret, MinIssuedAt: testIssuedAt}
	if _, err := withEpoch.Read(beforeEpoch); err == nil {
		t.Error("a token issued before the epoch was accepted")
	}
	if _, err := withEpoch.Read(tokenValid); err != nil {
		t.Errorf("a token issued at the epoch was refused: %v", err)
	}
	withoutEpoch := SessionToken{Secret: testSecret}
	if _, err := withoutEpoch.Read(beforeEpoch); err != nil {
		t.Errorf("with no epoch set the token must be accepted: %v", err)
	}
}

func TestSessionTokenReportsAForeignIssuer(t *testing.T) {
	// Verified or not, a token naming an issuer is not one of this
	// application's: its own tokens carry no iss claim. It must be reported
	// as somebody else's rather than refused as a bad session, so the
	// resolver hands it to the agent verifier and to nothing else.
	_, err := SessionToken{Secret: testSecret}.Read(tokenForeignIssuer)
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
		"the Python tier's own token":  {tokenWithoutSignIn, ""},
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
