// The mail transports in Go: the contract a sender delivers through, the MIME
// builder, and the SMTP and smtp2go transports. A module of its own, required
// with a relative replace by whichever Go tier sends mail, because internal
// packages cannot be shared across modules. soloclef_mail/AGENTS.md has the
// layout.
module git.danvics.com/danvics/soloclef/soloclef_mail

go 1.24.4
