package csrforigin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const signInPath = "/auth/jwt/login"

func sessionPolicy() Policy {
	return policy().GuardingSessionWrites(http.MethodPost + " " + signInPath)
}

func sessionRequest(method, path string, headers map[string]string) *http.Request {
	built := httptest.NewRequest(method, "http://api.example.invalid"+path, nil)
	for name, value := range headers {
		built.Header.Set(name, value)
	}
	return built
}

// A sign-in carries no session cookie -- that is what it is for -- so the
// cookie rule never looked at it, and a page anywhere could post its own
// address and password to it: the browser stores the cookies the answer
// sets, and the person is then working in the stranger's account and
// uploading into it (login CSRF). A request that writes the browser's
// session is checked by its origin whether or not a cookie came with it. A
// request that names no origin at all is a native app, which no web page can
// forge, and still passes.
func TestASessionWriteFromAForeignPageIsRefusedWithoutACookie(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		permit  bool
	}{
		{"a foreign page's sign-in is refused", http.MethodPost, signInPath,
			map[string]string{"Origin": "https://evil.example.invalid"}, false},
		{"so is a sandboxed frame's, whose origin is null", http.MethodPost, signInPath,
			map[string]string{"Origin": "null"}, false},
		{"and one that names its page only by Referer", http.MethodPost, signInPath,
			map[string]string{"Referer": "https://evil.example.invalid/form"}, false},
		{"this deployment's own sign-in page passes", http.MethodPost, signInPath,
			map[string]string{"Origin": "http://frontend.example.invalid"}, true},
		{"as does a page on the API's own host", http.MethodPost, signInPath,
			map[string]string{"Origin": "http://api.example.invalid"}, true},
		{"a native app, which names no origin, passes", http.MethodPost, signInPath, nil, true},
		{"with the cookie, the cookie rule still applies", http.MethodPost, signInPath,
			map[string]string{"Cookie": cookieName + "=x"}, false},
		{"a write that is not a session write keeps the cookie rule", http.MethodPost, "/api/v1/notes",
			map[string]string{"Origin": "https://evil.example.invalid"}, true},
		{"another method on the sign-in path is not the session write", http.MethodPut, signInPath,
			map[string]string{"Origin": "https://evil.example.invalid"}, true},
	}

	check := sessionPolicy()
	for _, test := range cases {
		if got := check.Permits(sessionRequest(test.method, test.path, test.headers)); got != test.permit {
			t.Errorf("%s: permitted=%v, want %v", test.name, got, test.permit)
		}
	}
}

// Without the session writes named, the policy is the cookie rule alone.
func TestAPolicyNamingNoSessionWritesIsTheCookieRule(t *testing.T) {
	request := sessionRequest(http.MethodPost, signInPath, map[string]string{"Origin": "https://evil.example.invalid"})
	if !policy().Permits(request) {
		t.Error("a cookieless write was refused by a policy that names no session writes")
	}
}
