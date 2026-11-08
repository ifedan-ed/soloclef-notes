package agentsignin

import (
	"errors"
	"testing"
)

// The rule that decides which account a sign-in acts as, with no database in
// the way. Every address here is at example.invalid: no account's own data
// belongs in a repository with a public copy.
func TestWhichAccountASignInActsAs(t *testing.T) {
	cases := map[string]struct {
		candidates Candidates
		want       string
	}{
		"an identity linked by an earlier web sign-in": {
			Candidates{LinkedUserID: "11111111-1111-1111-1111-111111111111"},
			"11111111-1111-1111-1111-111111111111",
		},
		"a linked identity whose address now belongs to someone else": {
			Candidates{
				LinkedUserID:      "11111111-1111-1111-1111-111111111111",
				EmailUserID:       "22222222-2222-2222-2222-222222222222",
				EmailVerified:     true,
				EmailUserVerified: true,
			},
			"11111111-1111-1111-1111-111111111111",
		},
		"an unlinked identity whose verified address has a verified account": {
			Candidates{
				EmailUserID:       "22222222-2222-2222-2222-222222222222",
				EmailVerified:     true,
				EmailUserVerified: true,
			},
			"22222222-2222-2222-2222-222222222222",
		},
		"an address the provider did not verify": {
			Candidates{
				EmailUserID:       "22222222-2222-2222-2222-222222222222",
				EmailVerified:     false,
				EmailUserVerified: true,
			},
			"",
		},
		"an account that never verified its own address": {
			Candidates{
				EmailUserID:       "22222222-2222-2222-2222-222222222222",
				EmailVerified:     true,
				EmailUserVerified: false,
			},
			"",
		},
		"a genuine sign-in with no account at all": {Candidates{EmailVerified: true}, ""},
	}

	for name, test := range cases {
		got, err := ChooseAccount(test.candidates)
		if test.want == "" {
			if !errors.Is(err, ErrNoAccount) {
				t.Errorf("%s: got %q/%v, want no account", name, got, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != test.want {
			t.Errorf("%s: got %q, want %q", name, got, test.want)
		}
	}
}
