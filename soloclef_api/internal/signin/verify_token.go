package signin

import (
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// verifyToken says who the request's credential is. It is behind the gate,
// so reaching it at all means the credential is good; the answer adds how it
// was proved and which account it is.
//
// It is a plain "who am I" for any credential, such as a script checking
// its API key; `method` says whether it is a session or an API key.
func (r Routes) verifyToken(response http.ResponseWriter, request *http.Request) {
	caller, ok := principal.FromContext(request.Context())
	if !ok {
		r.fail(response, request, "the route ran without an authenticated caller", nil)
		return
	}
	send(response, http.StatusOK, struct {
		Message string `json:"message"`
		Method  string `json:"method"`
		UserID  string `json:"user_id"`
	}{"Token is valid", string(caller.Method), caller.UserID})
}
