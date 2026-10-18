"""Microsoft 365 and Outlook.com mailboxes in the Email (IMAP) connector.

Microsoft turned off password sign-in for IMAP, so these mailboxes connect
through Microsoft's own sign-in and consent page (OAuth 2.0 authorization
code with PKCE) and IMAP then signs in with SASL XOAUTH2. Reading stays
read-only, the same client as every other mailbox. Set-up and limits:
``custom/docs/email-imap-connector.md`` ("Microsoft accounts").
"""
