package main

func init() { registerArea(signoutArea) }

// What ends a session, as the installed builds do it, after every other
// area: a device signed out from the devices list, a phone's own sign-out,
// a password change (which ends every other sign-in), Sign out everywhere,
// and deleting the account, which is the very last case of the contract.
// Each acts on a phone session signed in for it, so the session every other
// case used lives until it is meant to end. Password sign-ins are kept few
// (five in the whole contract, with the change of password): the server
// allows ten a minute from one client.
var signoutArea = contractArea{
	Name:  "signout",
	Order: 95,
	Cases: []contractCase{
		// Signed-in devices → Sign out (features/account/DevicesScreen.tsx).
		{Operation: "POST /auth/desktop/login", Path: "/auth/desktop/login", Anonymous: true, Status: 200,
			Body:    `{"email":"alice@example.test","password":"` + contractPassword + `","device_label":"Alice's old iPhone"}`,
			Capture: map[string]string{"old_phone_session": "session_id"}},
		{Operation: "DELETE /auth/devices/{session_id}", Path: "/auth/devices/{{old_phone_session}}", Status: 204},

		// The phone's own sign-out (lib/api/client.ts revoke): the refresh
		// token of the phone session the account area signed in, with no
		// access token.
		{Operation: "POST /auth/jwt/revoke", Path: "/auth/jwt/revoke", Anonymous: true, Status: 200,
			Body: `{"refresh_token":"{{phone_refresh}}"}`},

		// Change password (features/account/ChangePasswordScreen.tsx): the
		// sign-in that asks keeps working.
		{Operation: "POST /auth/change-password", Path: "/auth/change-password", Status: 200,
			Body: `{"current_password":"` + contractPassword + `","new_password":"` + contractPassword + `-changed"}`},

		// Sign out everywhere (lib/api/client.ts signOutEverywhere): the
		// access token and the refresh token of the phone that asks.
		{Operation: "POST /auth/desktop/login", Path: "/auth/desktop/login", Anonymous: true, Status: 200,
			Body:    `{"email":"alice@example.test","password":"` + contractPassword + `-changed","device_label":"Alice's iPhone"}`,
			Capture: map[string]string{"everywhere_access": "access_token", "everywhere_refresh": "refresh_token"}},
		{Operation: "POST /auth/jwt/logout-all", Path: "/auth/jwt/logout-all", Bearer: "{{everywhere_access}}", Status: 200,
			Body: `{"refresh_token":"{{everywhere_refresh}}"}`},

		// Delete account (features/account/DeleteAccountSheet.tsx), from a
		// fresh sign-in: the last request of the contract.
		{Operation: "POST /auth/desktop/login", Path: "/auth/desktop/login", Anonymous: true, Status: 200,
			Body:    `{"email":"alice@example.test","password":"` + contractPassword + `-changed","device_label":"Alice's iPhone"}`,
			Capture: map[string]string{"last_access": "access_token"}},
		{Operation: "DELETE /users/me", Path: "/users/me", Bearer: "{{last_access}}", Status: 204},
	},
}
