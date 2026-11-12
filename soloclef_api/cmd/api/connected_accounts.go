package main

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/config"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/driveconnect"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/microsoftmail"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
)

// The chat's mail and Drive tools use the app registrations an account's
// mailbox or Drive was connected through. Each is handed to the chat only
// when this deployment can connect one at all, by the very test its
// connect routes apply: a deployment whose Drive routes answer "not set
// up" offers no Drive tools, and never renews a grant with half a
// project's credentials.

// chatMicrosoftMail is where a Microsoft mailbox's token is refreshed, or
// nothing on a deployment that has not set Microsoft mail up.
func chatMicrosoftMail(settings config.Config) assembly.MicrosoftMailSettings {
	registration := microsoftmail.Settings{
		ClientID: settings.MicrosoftMailClientID, ClientSecret: settings.MicrosoftMailClientSecret,
		Tenant: settings.MicrosoftMailTenant, RedirectURI: settings.MicrosoftMailRedirectURI,
	}
	if !registration.Configured() {
		return assembly.MicrosoftMailSettings{}
	}
	return assembly.MicrosoftMailSettings{
		ClientID: settings.MicrosoftMailClientID, ClientSecret: settings.MicrosoftMailClientSecret,
		TokenURL: microsoftmail.TokenURL(settings.MicrosoftMailTenant),
	}
}

// chatGoogleDrive is the Google project a Drive grant is renewed at, or
// nothing on a deployment that has not set Drive up.
func chatGoogleDrive(settings config.Config) assembly.GoogleDriveSettings {
	project := driveconnect.Settings{
		ClientID: settings.GoogleOAuthClientID, ClientSecret: settings.GoogleOAuthClientSecret,
		RedirectURI: settings.GoogleDriveRedirectURI, PickerAPIKey: settings.GooglePickerAPIKey, PickerAppID: settings.GooglePickerAppID,
	}
	if len(project.Missing()) > 0 {
		return assembly.GoogleDriveSettings{}
	}
	return assembly.GoogleDriveSettings{ClientID: settings.GoogleOAuthClientID, ClientSecret: settings.GoogleOAuthClientSecret}
}
