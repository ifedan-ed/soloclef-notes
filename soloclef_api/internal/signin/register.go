package signin

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
)

// The sign-up form: an address and a password make an account.
//
// Whether it works at all is the deployment's SIGNUP_ALLOWED, the same switch
// a first single sign-on reads (Routes.SignupAllowed). With it off the route
// answers a refusal the page words as "sign-up is closed", and the operator
// makes accounts (`soloclef-api set-password`).
//
// The account it makes is unverified, because nothing here has shown that
// the person holds the address. That is what keeps single sign-on from
// handing somebody's identity to an account a stranger made in their name
// (internal/oidcsignin, link.go); a password reset by mail verifies it.

// The refusals, in the words the web app's sign-up page maps to sentences.
const (
	registerDisabled        = "REGISTER_DISABLED"
	registerInvalidEmail    = "REGISTER_INVALID_EMAIL"
	registerInvalidPassword = "REGISTER_INVALID_PASSWORD"
	registerAlreadyExists   = "REGISTER_USER_ALREADY_EXISTS"
)

// maxAddressLength is what the column holds.
const maxAddressLength = 320

// usableAddress is the address trimmed, when it is one mail could be sent
// to: a single bare address, with nothing around it. "Name <a@b>" parses as
// an address and is not one to store.
func usableAddress(raw string) (string, bool) {
	address := strings.TrimSpace(raw)
	if address == "" || len(address) > maxAddressLength {
		return "", false
	}
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Address != address || !strings.Contains(address[strings.LastIndex(address, "@")+1:], ".") {
		return "", false
	}
	return address, true
}

// registered is the new account as the sign-up page reads it. The three
// flags are the row's own: a request that names `is_superuser` names
// nothing, because the body's only fields read are the address and the
// password.
type registered struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	IsActive    bool   `json:"is_active"`
	IsSuperuser bool   `json:"is_superuser"`
	IsVerified  bool   `json:"is_verified"`
}

func (r Routes) register(response http.ResponseWriter, request *http.Request) {
	if !r.SignupAllowed {
		r.refuse(response, request, http.StatusForbidden, registerDisabled)
		return
	}
	body, ok := readBody(request)
	if !ok {
		r.refuse(response, request, http.StatusRequestEntityTooLarge, "The sign-up request is too large.")
		return
	}
	var asked struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(body, &asked); err != nil {
		r.refuse(response, request, http.StatusBadRequest, registerInvalidEmail)
		return
	}
	address, ok := usableAddress(asked.Email)
	if !ok {
		r.refuse(response, request, http.StatusBadRequest, registerInvalidEmail)
		return
	}
	if err := checkNewPassword(address, asked.Password); err != nil {
		r.refuse(response, request, http.StatusBadRequest, registerInvalidPassword)
		return
	}
	ctx := request.Context()
	hash, err := r.Hasher.Hash(ctx, asked.Password)
	if err != nil {
		r.fail(response, request, "could not hash the password", err)
		return
	}
	row, err := New(r.Pool).RegisterAccount(ctx, RegisterAccountParams{Email: address, HashedPassword: &hash})
	if isNoRows(err) {
		// Said outright, as every open sign-up form says it: the person
		// needs to know to sign in instead. The per-client limit on this
		// route is what keeps it from being a fast way to list accounts.
		r.refuse(response, request, http.StatusBadRequest, registerAlreadyExists)
		return
	}
	if err != nil {
		r.fail(response, request, "could not make the account", err)
		return
	}
	send(response, http.StatusCreated, registered{
		ID: row.ID, Email: row.Email, IsActive: row.IsActive,
		IsSuperuser: row.IsSuperuser, IsVerified: row.IsVerified,
	})
}
