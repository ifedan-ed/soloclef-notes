package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/automationmail"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/config"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer/smtp"
)

// How the results of automation runs are mailed, as opposed to where the
// mail server is: decisions about the sender, made here with the reason.
const (
	// A result is in its owner's inbox within a few seconds of the run
	// ending; the look is one indexed statement that finds nothing most
	// of the time.
	automationMailEvery = 5 * time.Second
	// Ten results a look is more than runs end in five seconds; whatever
	// is left is the next look's.
	automationMailBatch = 10
)

// startAutomationMail starts the sender of automation results
// (internal/automationmail) for as long as the server lives.
//
// It has an SMTP client of its own, built from the settings the password
// reset's is built from: a client carries one message at a time, and a
// reset must not wait behind a batch of results. Where the deployment
// sends no mail the sender still runs, without a client, so that a run
// left waiting by an automation saved before mail was switched off is
// said to be not sent instead of waiting for ever.
func startAutomationMail(lifetime context.Context, settings config.Config, pool *pgxpool.Pool, logger *slog.Logger) error {
	sender := &automationmail.Sender{
		Rows: automationmail.Store{Pool: pool}, FrontendURL: settings.FrontendURL, Brand: settings.BrandName,
		Every: automationMailEvery, Batch: automationMailBatch, Logger: logger, Now: time.Now,
	}
	if settings.SMTPEnabled {
		client, warning, err := smtp.New(smtp.Settings{
			Host: settings.SMTPHost, Port: settings.SMTPPort, Security: smtp.Security(settings.SMTPSecurity),
			Username: settings.SMTPUsername, Password: settings.SMTPPassword, From: settings.SMTPFrom, Timeout: settings.SMTPTimeout,
		})
		if err != nil {
			return err
		}
		if warning != "" {
			logger.Warn(warning)
		}
		sender.Mail = client
	}
	if err := sender.Check(); err != nil {
		return err
	}
	go sender.Run(lifetime)
	return nil
}
