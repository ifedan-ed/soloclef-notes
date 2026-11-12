package signin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// newRefreshToken is 32 random bytes as URL-safe text: what a client holds.
// Only its hash is stored (hashToken).
func newRefreshToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// hashToken is the stored form of a refresh token. A plain SHA-256 is
// enough: the token is 256 random bits, so there is nothing to guess.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newFamilyID is a random (version 4) uuid naming one sign-in.
func newFamilyID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// issued is a new sign-in's first refresh token and the family it starts.
type issued struct {
	token    string
	familyID string
}

// startFamily records a new sign-in for the account and answers its first
// refresh token. A phone names itself (device), which is shown in the
// devices list.
func startFamily(ctx context.Context, db DBTX, userID, kind string, device *string, now time.Time) (issued, error) {
	token, err := newRefreshToken()
	if err != nil {
		return issued{}, err
	}
	family, err := newFamilyID()
	if err != nil {
		return issued{}, err
	}
	familyCap := now.Add(familyLifetime(kind))
	err = New(db).InsertRefreshToken(ctx, InsertRefreshTokenParams{
		UserID:         userID,
		TokenHash:      hashToken(token),
		ExpiresAt:      tokenExpiry(kind, now, familyCap),
		FamilyID:       family,
		AbsoluteExpiry: &familyCap,
		Kind:           kind,
		DeviceLabel:    device,
		SignedInAt:     &now,
		CreatedAt:      now,
	})
	if err != nil {
		return issued{}, fmt.Errorf("recording the sign-in: %w", err)
	}
	return issued{token: token, familyID: family}, nil
}

// rotation is what a refresh was granted. An empty token means the grace
// case: an access token is owed and the client keeps the refresh token it
// has, whose successor another of its requests already holds.
type rotation struct {
	userID   string
	familyID string
	token    string
}

// errRefreshRefused is every refresh token that does not work.
var errRefreshRefused = errors.New("refresh token refused")

// rotate spends a refresh token and mints its successor, in one transaction
// that holds the presented token's row locked.
//
// A spent token presented within rotationGrace of being spent (and before it
// would have expired anyway) is a client that raced itself, and is owed an
// access token only. A spent token presented later is a copy: the family is
// ended, the phone it belongs to stops getting pushes, and the refusal is
// committed with it so a retry finds the family already gone.
func rotate(ctx context.Context, pool *pgxpool.Pool, token string, now time.Time) (rotation, error) {
	type outcome struct {
		granted rotation
		refused bool
	}
	result, err := database.Write(ctx, pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (outcome, error) {
		q := New(tx)
		row, err := q.LockRefreshToken(ctx, hashToken(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return outcome{refused: true}, nil
		}
		if err != nil {
			return outcome{}, err
		}
		if row.RevokedAt != nil {
			if now.Sub(*row.RevokedAt) <= rotationGrace && now.Before(row.ExpiresAt) {
				return outcome{granted: rotation{userID: row.UserID, familyID: row.FamilyID}}, nil
			}
			if err := endSignIn(ctx, q, row.UserID, row.FamilyID, now); err != nil {
				return outcome{}, err
			}
			return outcome{refused: true}, nil
		}
		familyCap := now.Add(familyLifetime(row.Kind))
		if row.AbsoluteExpiry != nil {
			familyCap = *row.AbsoluteExpiry
		}
		if !now.Before(row.ExpiresAt) || !now.Before(familyCap) {
			return outcome{refused: true}, nil
		}

		successor, err := newRefreshToken()
		if err != nil {
			return outcome{}, err
		}
		// The successor keeps the family's kind, device and sign-in time:
		// a phone is the same phone, signed in when it was, across every
		// refresh.
		signedIn := row.SignedInAt
		if signedIn == nil {
			signedIn = &row.CreatedAt
		}
		if err := q.InsertRefreshToken(ctx, InsertRefreshTokenParams{
			UserID:         row.UserID,
			TokenHash:      hashToken(successor),
			ExpiresAt:      tokenExpiry(row.Kind, now, familyCap),
			FamilyID:       row.FamilyID,
			AbsoluteExpiry: &familyCap,
			Kind:           row.Kind,
			DeviceLabel:    row.DeviceLabel,
			SignedInAt:     signedIn,
			CreatedAt:      now,
		}); err != nil {
			return outcome{}, err
		}
		if err := q.RetireRefreshToken(ctx, RetireRefreshTokenParams{RevokedAt: &now, ID: row.ID, UserID: row.UserID}); err != nil {
			return outcome{}, err
		}
		return outcome{granted: rotation{userID: row.UserID, familyID: row.FamilyID, token: successor}}, nil
	})
	if err != nil {
		return rotation{}, err
	}
	if result.refused {
		return rotation{}, errRefreshRefused
	}
	return result.granted, nil
}

// endByToken signs out the device that holds this refresh token: its whole
// family, and the pushes to its phone. It reports whether the token named a
// sign-in at all.
func endByToken(ctx context.Context, pool *pgxpool.Pool, token string, now time.Time) (bool, error) {
	return database.Write(ctx, pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (bool, error) {
		q := New(tx)
		ended, err := q.EndTokenByHash(ctx, EndTokenByHashParams{EndedAt: &now, TokenHash: hashToken(token)})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, endSignIn(ctx, q, ended.UserID, ended.FamilyID, now)
	})
}

// endSignIn ends every token of one sign-in and forgets the phone that
// registered for pushes under it, so a signed-out phone is not woken by the
// account's notifications.
func endSignIn(ctx context.Context, q *Queries, userID, familyID string, now time.Time) error {
	if _, err := q.EndFamily(ctx, EndFamilyParams{EndedAt: now, FamilyID: familyID, UserID: userID}); err != nil {
		return err
	}
	return q.ForgetPushDevices(ctx, ForgetPushDevicesParams{UserID: userID, FamilyID: &familyID})
}
