package main

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/config"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/oidcsignin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signin"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer/smtp"
)

// resetMailer is what sends the password-reset link: an SMTP client built
// from the deployment's settings, or nothing where the deployment sends no
// mail (a reset is then refused in so many words, internal/signin).
//
// This tier had no mailer of its own. It does not join the mail queue the
// Python tier's senders drain, because the Go stack runs no sender to drain
// it; it sends the one kind of mail it has, directly, through the SMTP
// transport soloclef_mail already has and tests. One transport, named by
// the settings: there is no second way mail leaves if this one fails.
func resetMailer(settings config.Config, logger *slog.Logger) (signin.Mailer, error) {
	if !settings.SMTPEnabled {
		return nil, nil
	}
	client, warning, err := smtp.New(smtp.Settings{
		Host:     settings.SMTPHost,
		Port:     settings.SMTPPort,
		Security: smtp.Security(settings.SMTPSecurity),
		Username: settings.SMTPUsername,
		Password: settings.SMTPPassword,
		From:     settings.SMTPFrom,
		Timeout:  settings.SMTPTimeout,
	})
	if err != nil {
		return nil, err
	}
	if warning != "" {
		logger.Warn(warning)
	}
	return client, nil
}

// singleSignOn is the OpenID Connect routes (internal/oidcsignin): the
// website's two and the phone app's two. They are mounted whether or not
// the deployment has single sign-on, and answer 404 where it has not. A
// session they start is made by `sessions`, the password sign-in's own
// routes, so it is the same session.
//
// The app's one-time codes are kept in the deployment's Valkey, shared by
// every replica (oidcsignin.ValkeyAppCodes). It connects on first use, so a
// Valkey that is down does not stop this process starting.
func singleSignOn(settings config.Config, pool *pgxpool.Pool, sessions signin.Routes,
	errors errorenvelope.Writer, logger *slog.Logger) (oidcsignin.Routes, error) {
	routes := oidcsignin.Routes{Enabled: settings.OIDCEnabled, Errors: errors, Logger: logger}
	if !settings.OIDCEnabled {
		return routes, nil
	}
	codes, err := oidcsignin.NewValkeyAppCodes(settings.RedisURL)
	if err != nil {
		return routes, err
	}
	routes.Settings = oidcsignin.Settings{
		ClientID:     settings.OIDCClientID,
		ClientSecret: settings.OIDCClientSecret,
		RedirectURL:  settings.OIDCRedirectURL,
		FrontendURL:  settings.FrontendURL,
		// The name agent sign-in looks identities up under, so an identity
		// linked by the web's sign-in is the one an agent's token finds.
		ProviderName:  settings.AgentProviderName,
		SignupAllowed: settings.SignupAllowed,
		AppRedirects:  settings.OIDCAppRedirects,
	}
	routes.Pool = pool
	routes.Verifier = oidcsignin.NewVerifier(settings.OIDCIssuer, settings.OIDCClientID, settings.AgentProviderName)
	routes.Sessions = sessions
	routes.Secret = settings.SessionSecret
	routes.Codes = codes
	return routes, nil
}
