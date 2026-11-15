package maildrafts

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	ownerA = "aaaaaaaa-0000-4000-8000-000000000001"
	ownerB = "bbbbbbbb-0000-4000-8000-000000000002"
)

var clock = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

func sample() Message {
	return Message{AccountID: "acc", IdentityID: "I1", From: Address{Name: "Ada", Email: "ada@example.test"},
		To: []Address{{Name: "Ben", Email: "ben@example.test"}}, Subject: "Lunch", Body: "Noon at the usual place?"}.canonical()
}

// admitted is an admission that passes, for each case to break one part of.
func admitted() admission {
	message := sample()
	return admission{
		OwnerID: ownerA, DraftID: "D1", PresentedHash: message.ContentHash(),
		Confirmation: &confirmation{DraftID: "D1", OwnerID: ownerA, ContentHash: message.ContentHash(), ExpiresAt: clock.Add(time.Minute)},
		Draft:        Draft{ID: "D1", Message: message, Status: StatusPending, ExpiresAt: clock.Add(time.Hour)},
		Now:          clock,
	}
}

func TestTheGuardAdmitsOnlyAConfirmedUnchangedDraft(t *testing.T) {
	if err := admit(admitted()); err != nil {
		t.Fatalf("a valid send was refused: %v", err)
	}
	cases := map[string]struct {
		change func(*admission)
		want   error
	}{
		"no go-ahead at all":           {func(a *admission) { a.Confirmation = nil }, ErrConfirmationInvalid},
		"another account's go-ahead":   {func(a *admission) { a.Confirmation.OwnerID = ownerB }, ErrConfirmationInvalid},
		"another account sending":      {func(a *admission) { a.OwnerID = ownerB }, ErrConfirmationInvalid},
		"a go-ahead for another draft": {func(a *admission) { a.Confirmation.DraftID = "D2" }, ErrConfirmationInvalid},
		"a replayed go-ahead":          {func(a *admission) { a.Confirmation.Used = true }, ErrConfirmationSpent},
		"a go-ahead that ran out":      {func(a *admission) { a.Now = a.Confirmation.ExpiresAt }, ErrConfirmationExpired},
		"a sent draft":                 {func(a *admission) { a.Draft.Status = StatusSent }, ErrDraftNotPending},
		"a superseded draft":           {func(a *admission) { a.Draft.Status = StatusSuperseded }, ErrDraftNotPending},
		"an expired draft":             {func(a *admission) { a.Draft.ExpiresAt = clock }, ErrDraftNotPending},
		"a recipient added after it was shown": {func(a *admission) {
			a.Draft.Message.Bcc = []Address{{Email: "evil@example.com"}}
		}, ErrDraftChanged},
		"one character of the body changed": {func(a *admission) { a.Draft.Message.Body += "!" }, ErrDraftChanged},
		"the browser showed something else": {func(a *admission) { a.PresentedHash = "sha256:00" }, ErrDraftChanged},
		// The pace became unusual after the card was drawn: the go-ahead
		// was issued without the point, so the person has not seen it.
		"the past hour became unusual since the card was drawn": {func(a *admission) { a.SentLastHour = UnusualSendsPerHour }, ErrUnusualChanged},
		"an unusual point not ticked": {func(a *admission) {
			a.Confirmation.Unusual = []string{UnusualNewRecipients}
		}, ErrUnusualNotConfirmed},
		"one of two unusual points ticked": {func(a *admission) {
			a.Confirmation.Unusual = []string{UnusualManySends, UnusualNewRecipients}
			a.SentLastHour = UnusualSendsPerHour + 5
			a.Acknowledged = []string{UnusualManySends}
		}, ErrUnusualNotConfirmed},
	}
	for name, c := range cases {
		a := admitted()
		copied := *a.Confirmation
		a.Confirmation = &copied
		c.change(&a)
		if err := admit(a); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}

// manyRecipients is the sample with more recipients than is usual.
func manyRecipients() Message {
	message := sample()
	for index := range UnusualRecipients {
		message.Cc = append(message.Cc, Address{Email: "person" + string(rune('a'+index)) + "@example.test"})
	}
	return message
}

func TestAnUnusualSendNeedsEveryPointTickedAndIsNeverRefusedForIt(t *testing.T) {
	// Twenty-one recipients and a twenty-first send within the hour are
	// valid messages: they are asked about twice, not refused.
	many := manyRecipients()
	if err := many.validate(); err != nil {
		t.Fatalf("a message to %d recipients is refused: %v", len(many.Recipients()), err)
	}
	a := admitted()
	a.Draft.Message, a.PresentedHash = many, many.ContentHash()
	a.Confirmation.ContentHash = many.ContentHash()
	a.SentLastHour = UnusualSendsPerHour
	// Issued without the points (the card never showed them): refused
	// until it is drawn again with them.
	if err := admit(a); !errors.Is(err, ErrUnusualChanged) {
		t.Fatalf("points the go-ahead did not carry: %v", err)
	}
	// Issued with them, nothing ticked: the second confirmation is the
	// server's to enforce, not only the card's.
	a.Confirmation.Unusual = codes(append(fromContent(many), fromPace(a.SentLastHour)...))
	if strings.Join(a.Confirmation.Unusual, ",") != UnusualManyRecipients+","+UnusualManySends {
		t.Fatalf("the points = %v", a.Confirmation.Unusual)
	}
	if err := admit(a); !errors.Is(err, ErrUnusualNotConfirmed) {
		t.Fatalf("nothing ticked: %v", err)
	}
	// Every point ticked: it sends. A code ticked that was not asked about
	// changes nothing.
	a.Acknowledged = []string{UnusualManySends, "not_a_point", UnusualManyRecipients}
	if err := admit(a); err != nil {
		t.Fatalf("every point ticked: %v", err)
	}
	// The ticks are bound to that go-ahead: a used one is refused first,
	// whatever is ticked.
	a.Confirmation.Used = true
	if err := admit(a); !errors.Is(err, ErrConfirmationSpent) {
		t.Fatalf("a replay with the points ticked: %v", err)
	}
}

func TestWhatIsUnusualAboutAMessage(t *testing.T) {
	if points := fromContent(sample()); len(points) != 0 {
		t.Fatalf("an everyday message has points: %+v", points)
	}
	exactly := sample()
	for len(exactly.Recipients()) < UnusualRecipients {
		exactly.Cc = append(exactly.Cc, Address{Email: "p" + string(rune('a'+len(exactly.Cc))) + "@example.test"})
	}
	if points := fromContent(exactly); len(points) != 0 {
		t.Fatalf("exactly %d recipients is unusual: %+v", UnusualRecipients, points)
	}
	if points := fromContent(manyRecipients()); len(points) != 1 || points[0].Count != UnusualRecipients+1 {
		t.Fatalf("many recipients = %+v", points)
	}
	large := sample()
	large.Attachments = []Attachment{testAttachment("a.pdf", UnusualAttachmentBytes/2), testAttachment("b.pdf", UnusualAttachmentBytes/2+1)}
	if points := fromContent(large); len(points) != 1 || points[0].Code != UnusualLargeAttachments || points[0].Bytes != UnusualAttachmentBytes+1 {
		t.Fatalf("large attachments = %+v", points)
	}
	if fromPace(UnusualSendsPerHour-1) != nil || fromPace(UnusualSendsPerHour)[0].Code != UnusualManySends {
		t.Fatal("the pace point is not at the threshold")
	}
	// Replying to everyone on the message answered is not writing to
	// anyone new, and neither is writing to oneself.
	reply := sample()
	reply.ThreadParticipants = []string{"ben@example.test"}
	reply.Cc = []Address{{Email: "ADA@example.test"}, {Email: "carol@example.test"}}
	if checked := recipientsToCheck(reply); len(checked) != 1 || checked[0] != "carol@example.test" {
		t.Fatalf("checked = %v", checked)
	}
	if points := fromHistory(nil, []string{"x@example.test"}); points[0].Code != UnusualUncheckedRecipients {
		t.Fatal("a history that could not be read is not asked about")
	}
}

// testAttachment is a valid attachment of size bytes.
func testAttachment(name string, size int64) Attachment {
	return Attachment{Name: name, Type: "application/pdf", Size: size, SHA256: strings.Repeat("ab", 32),
		Origin: "Document: " + name, Source: AttachmentSource{Kind: SourceDocument, FileID: 7}}
}

func TestTheContentHashCoversEveryFieldAndOnlyTheContent(t *testing.T) {
	base := sample()
	// nil and empty lists are one content.
	same := base
	same.Cc = nil
	if same.ContentHash() != base.ContentHash() {
		t.Fatal("an empty list and a missing one hash differently")
	}
	// A stored message read back (jsonb reorders keys) hashes as it was.
	encoded, _ := base.encoded()
	var keyed map[string]any
	_ = json.Unmarshal(encoded, &keyed)
	reordered, _ := json.Marshal(keyed) // keys sorted, as jsonb may give them
	back, err := decodeMessage(reordered)
	if err != nil || back.ContentHash() != base.ContentHash() {
		t.Fatalf("a message read back hashes differently: %v", err)
	}
	changes := []func(*Message){
		func(m *Message) { m.Subject = "Lunch!" },
		func(m *Message) { m.To[0].Name = "Benjamin" },
		func(m *Message) { m.From.Email = "other@example.test" },
		func(m *Message) { m.IdentityID = "I2" },
		func(m *Message) { m.Attachments = []Attachment{testAttachment("a.pdf", 1)} },
		// Another file under the same name and size: its digest differs.
		func(m *Message) {
			changed := testAttachment("a.pdf", 1)
			changed.SHA256 = strings.Repeat("cd", 32)
			m.Attachments = []Attachment{changed}
		},
		func(m *Message) {
			moved := testAttachment("a.pdf", 1)
			moved.Source.FileID = 8
			m.Attachments = []Attachment{moved}
		},
		func(m *Message) { m.InReplyTo = []string{"x@example"} },
	}
	for index, change := range changes {
		changed := sample()
		changed.To = append([]Address(nil), changed.To...)
		change(&changed)
		if changed.ContentHash() == base.ContentHash() {
			t.Errorf("change %d did not change the hash", index)
		}
	}
}

func TestADraftThatIsNotAMessageIsRefused(t *testing.T) {
	withFile := func(change func(*Attachment)) Message {
		m := sample()
		file := testAttachment("a.pdf", 10)
		change(&file)
		m.Attachments = []Attachment{file}
		return m
	}
	for name, message := range map[string]Message{
		"no recipient":                    func() Message { m := sample(); m.To = []Address{}; return m }(),
		"a file name with a line break":   withFile(func(a *Attachment) { a.Name = "a.pdf\r\nBcc: x@example.com" }),
		"a file name that is a path":      withFile(func(a *Attachment) { a.Name = "../a.pdf" }),
		"an empty file":                   withFile(func(a *Attachment) { a.Size = 0 }),
		"a file without its digest":       withFile(func(a *Attachment) { a.SHA256 = "" }),
		"a file from nowhere":             withFile(func(a *Attachment) { a.Source = AttachmentSource{Kind: "url"} }),
		"a forwarded file without a blob": withFile(func(a *Attachment) { a.Source = AttachmentSource{Kind: SourceMail, AccountID: "a", MessageID: "m"} }),
		"a header in the subject": func() Message {
			m := sample()
			m.Subject = "Hi\r\nBcc: evil@example.com"
			return m
		}(),
		"a recipient twice": func() Message { m := sample(); m.Cc = []Address{{Email: "BEN@example.test"}}; return m }(),
		"no text":           func() Message { m := sample(); m.Body = " "; return m }(),
	} {
		if err := message.validate(); !IsInvalid(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, text := range []string{"a@b@c", "Ada <ada@example.test>, Ben <ben@example.test>", "no-at-sign"} {
		if _, err := ParseAddress(text); err == nil {
			t.Errorf("%q was taken as one address", text)
		}
	}
	if address, err := ParseAddress(" Ada Lovelace <ada@example.test> "); err != nil || address.Name != "Ada Lovelace" {
		t.Errorf("a named address: %+v, %v", address, err)
	}
}
