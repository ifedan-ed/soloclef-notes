package e2eseed

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// account finds the account an address names, or answers that there is
// none. The suite's accounts are made by the stack before it runs.
func (x *exchange) account(email string) (string, bool) {
	userID, err := New(x.routes.Pool).AccountByAddress(x.ctx.Context(), strings.TrimSpace(email))
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, "no account has the address "+email)
		return "", false
	}
	if err != nil {
		x.fail("could not look for the account", err)
		return "", false
	}
	return userID, true
}

// token answers an access token for an account, as a sign-in would, so a
// suite of many workers and retries does not meet the sign-in limit that
// exists to slow a password guesser.
func (r Routes) token(x *exchange) {
	var asked struct {
		Email string `json:"email"`
	}
	if !x.body(&asked) {
		return
	}
	if strings.TrimSpace(asked.Email) == "" {
		x.refuse(http.StatusUnprocessableEntity, "email is required: the account to mint a token for")
		return
	}
	userID, ok := x.account(asked.Email)
	if !ok {
		return
	}
	minted, err := r.MintSession(x.ctx.Context(), userID, r.now())
	if err != nil {
		x.fail("could not mint the token", err)
		return
	}
	x.send(http.StatusOK, map[string]string{"access_token": minted, "token_type": "bearer"})
}

// notifications puts rows in an account's inbox, as the handlers that
// write them would, so the panel's Delete and Clear all can be tried on a
// known inbox.
func (r Routes) notifications(x *exchange) {
	var asked struct {
		Email         string `json:"email"`
		Notifications []struct {
			Type       string         `json:"type"`
			Title      string         `json:"title"`
			Message    *string        `json:"message"`
			Read       bool           `json:"read"`
			Metadata   map[string]any `json:"metadata"`
			MinutesAgo int            `json:"minutes_ago"`
			// Running is a job still in progress: the inbox will not
			// delete such a row, which is what a test of that asks for.
			Running bool `json:"running"`
		} `json:"notifications"`
	}
	if !x.body(&asked) {
		return
	}
	userID, ok := x.account(asked.Email)
	if !ok {
		return
	}
	now := r.now()
	ids := make([]int64, 0, len(asked.Notifications))
	err := pgx.BeginFunc(x.ctx.Context(), r.Pool, func(tx pgx.Tx) error {
		q := New(tx)
		for _, row := range asked.Notifications {
			if row.Type == "" || row.Title == "" {
				return errInvalid("every notification needs a type and a title")
			}
			metadata := row.Metadata
			if metadata == nil {
				metadata = map[string]any{}
			}
			if row.Running {
				metadata["status"] = "in_progress"
			}
			encoded, err := json.Marshal(metadata)
			if err != nil {
				return err
			}
			message := "E2E notification"
			if row.Message != nil {
				message = *row.Message
			}
			id, err := q.SeedNotification(x.ctx.Context(), SeedNotificationParams{
				UserID: userID, Type: row.Type, Title: row.Title, Message: message, Read: row.Read,
				Metadata: encoded, CreatedAt: now.Add(-time.Duration(row.MinutesAgo) * time.Minute),
			})
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if x.failed("could not seed the notifications", err) {
		return
	}
	x.send(http.StatusCreated, map[string]any{"ids": ids})
}

// errInvalid is a seed request that names something this route cannot
// seed: a 422 with the reason, where every other failure is a 500.
type errInvalid string

func (e errInvalid) Error() string { return string(e) }

// errAbsent is a seed request that names a row that is not there: a 404.
type errAbsent string

func (e errAbsent) Error() string { return string(e) }

// failed answers for a seed that did not happen and reports whether it
// did so.
func (x *exchange) failed(reason string, err error) bool {
	var invalid errInvalid
	var absent errAbsent
	switch {
	case err == nil:
		return false
	case errors.As(err, &invalid):
		x.refuse(http.StatusUnprocessableEntity, string(invalid))
	case errors.As(err, &absent):
		x.refuse(http.StatusNotFound, string(absent))
	default:
		x.fail(reason, err)
	}
	return true
}
