package assembly

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailattachments"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
)

// JMAP mailboxes' timing: a conversation with the server (finding the
// session and one call) has the time an IMAP conversation has; one OAuth
// request (metadata, a code or a refresh) has fifteen seconds; a TCP
// connect, ten, as a connected app's. A conversation that moves a file (an
// attachment read or forwarded, a send that uploads files) has the time an
// IMAP attachment has.
const (
	jmapConversation = mailConversation
	jmapAttachment   = mailAttachment
	jmapOAuthRequest = 15 * time.Second
	jmapConnect      = connectedAppConnect
)

// NewJMAPMail is an account's JMAP mailboxes as every tier opens them: the
// API's connect and send routes and the chat's mail tools, in the API and
// in the worker, so that a token is refreshed one way everywhere. secret
// is the deployment's secret key, which the credentials are sealed under.
func NewJMAPMail(pool *pgxpool.Pool, secret string, logger *slog.Logger) (*jmapaccounts.Mail, error) {
	seal, err := jmapaccounts.NewSeal(secret)
	if err != nil {
		return nil, err
	}
	network := jmapaccounts.PublicNetwork{Internet: mcptools.PublicInternet{Resolve: mcptools.SystemResolve, ConnectTimeout: jmapConnect}}
	if err := network.Check(); err != nil {
		return nil, err
	}
	mail := &jmapaccounts.Mail{
		SignIn: jmapaccounts.SignIn{Rows: jmapaccounts.Store{Pool: pool}, Seal: seal,
			OAuth: jmapaccounts.OAuth{Network: network, Timeout: jmapOAuthRequest}, Now: time.Now, Logger: logger},
		Client: jmapmail.Client{Network: network, Timeout: jmapConversation, AttachmentTimeout: jmapAttachment},
	}
	return mail, mail.Check()
}

// NewDraftFiles is where every tier finds a draft's files and reads them
// back: the chat attaches with it, the send guard checks and sends with
// it. stores is the process's file store (every backend its rows name).
func NewDraftFiles(pool *pgxpool.Pool, stores mailattachments.Stores, jmap *jmapaccounts.Mail) (*mailattachments.Files, error) {
	files := &mailattachments.Files{Pool: pool, Stores: stores, Mail: jmap}
	return files, files.Check()
}

// NewDraftGuard is the send guard every tier uses: the chat stores drafts
// with it, and the API's confirmation routes show and send them. files
// reads a draft's attachments; notify is told when a draft starts waiting
// for its owner.
func NewDraftGuard(pool *pgxpool.Pool, jmap *jmapaccounts.Mail, files *mailattachments.Files, notify maildrafts.Notifier, logger *slog.Logger) (*maildrafts.Guard, error) {
	sender := maildrafts.JMAPSender{Mail: jmap, Files: files}
	guard := &maildrafts.Guard{
		Rows: maildrafts.Store{Pool: pool}, Accounts: sender,
		Senders: map[string]maildrafts.Sender{maildrafts.ProviderJMAP: sender},
		History: sender, Files: files,
		Notify: notify, Now: time.Now, Logger: logger,
	}
	return guard, guard.Check()
}
