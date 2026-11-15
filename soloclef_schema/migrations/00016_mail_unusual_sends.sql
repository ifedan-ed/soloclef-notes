-- +goose Up

-- An unusual send is confirmed twice instead of refused.
--
-- The send guard (soloclef_assistant/maildrafts) used to refuse a message
-- to more than 20 recipients and a 21st send within an hour. It now sends
-- either, once the person has also ticked each unusual point the card
-- names: many recipients, many sends in the past hour, recipients the
-- mailbox has never written to, large attachments. The points a go-ahead
-- was issued with are stored on it, so the second confirmation is bound to
-- the same single-use go-ahead and enforced by the server: a send that
-- does not acknowledge every one of them is refused.
--
-- A go-ahead lives ten minutes, so the ones live during the upgrade simply
-- carry none and are spent or expire as before.
ALTER TABLE mail_send_confirmations ADD COLUMN unusual text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE mail_send_confirmations DROP COLUMN unusual;
