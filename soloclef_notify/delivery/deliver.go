package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushpolicy"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushsend"
)

// What every push carries that is not the row's own.
const (
	// androidChannel is the channel the phone app makes at start.
	androidChannel = "soloclef"
	// payloadVersion is data.v, which the app reads to know the shape.
	payloadVersion = 1
	// receiptsFence is the fence key of the scheduled receipts check.
	receiptsFence = "soloclef:notify:receipts-scheduled"
	// pushTTL is how long a service keeps trying a phone that is off. A
	// push says "your video is ready": a day later the inbox says it better
	// than a buzz does, so a phone off for longer gets the row and no
	// stale push.
	pushTTL = 24 * time.Hour
)

// Deliver is notifications.deliver_push: push a batch of inbox rows, to
// every live phone of each owner, or on a retry to the phones it names.
func (j *Jobs) Deliver(ctx context.Context, msg *celery.Message) error {
	ids, err := idsOf(msg)
	if err != nil {
		return err
	}
	only, err := phonesOf(msg, ids)
	if err != nil {
		return err
	}
	return j.deliver(ctx, ids, only, msg.Retries)
}

// deliver pushes the rows; only is nil for every live phone, and attempt is
// how many times they were tried before.
func (j *Jobs) deliver(ctx context.Context, ids []int64, only phones, attempt int) error {
	now := j.now()
	q := New(j.Database)
	rows, err := q.Pushable(ctx, ids)
	if err != nil {
		return fmt.Errorf("delivery: reading rows %v: %w", ids, err)
	}
	wanted, err := j.allowed(ctx, rows, now)
	if errors.Is(err, errPresence) {
		j.Log.Warn("presence could not be read; the push will be tried again", "rows", ids, "error", err.Error())
		return j.again(ctx, ids, only, attempt)
	}
	if err != nil || len(wanted) == 0 {
		return err
	}
	owners := ownersOf(wanted)
	devices, err := q.LiveDevices(ctx, LiveDevicesParams{UserIds: owners, Now: now})
	if err != nil {
		return fmt.Errorf("delivery: reading phones: %w", err)
	}
	if len(devices) == 0 {
		return nil
	}
	counts, err := q.UnreadCounts(ctx, owners)
	if err != nil {
		return fmt.Errorf("delivery: counting unread rows: %w", err)
	}
	pending, left := j.messages(wanted, devices, counts, only)
	j.logLeftOut(left)

	failed := phones{}
	var receiptIDs, receiptTokens, gone []string
	for _, call := range pack(pending) {
		tickets, err := j.sender(call.route).Send(ctx, call.messages)
		switch {
		case errors.Is(err, pushsend.ErrTransient):
			j.Log.Warn("the push service did not take a request; it will be tried again", "via", call.route.String(), "rows", distinct(call.rows), "error", err.Error())
			for i := range call.messages {
				failed.add(call.rows[i], call.devices[i])
			}
			continue
		case err != nil:
			// It would be refused again; the rows are in the inbox.
			j.Log.Error("the push service refused a request", "via", call.route.String(), "rows", distinct(call.rows), "error", err.Error())
			continue
		case len(tickets) != len(call.messages):
			j.Log.Error("the push service answered for another number of pushes", "via", call.route.String(), "pushes", len(call.messages), "tickets", len(tickets))
			continue
		}
		retried, firstRetry := 0, ""
		for i, ticket := range tickets {
			token := call.messages[i].To
			switch {
			case ticket.Accepted:
				if ticket.ReceiptID != "" {
					receiptIDs, receiptTokens = append(receiptIDs, ticket.ReceiptID), append(receiptTokens, token)
				}
				j.Log.Debug("pushed", "via", call.route.String(), "notification", call.rows[i], "reference", ticket.Reference)
			case ticket.Gone:
				gone = append(gone, token)
				j.Log.Debug("the push service says a phone is gone", "via", call.route.String(), "error", ticket.Error, "reference", ticket.Reference)
			case ticket.Retry:
				failed.add(call.rows[i], call.devices[i])
				if retried++; firstRetry == "" {
					firstRetry = ticket.Error + " " + ticket.Reference
				}
			default:
				j.Log.Warn("a push was not accepted", "via", call.route.String(), "error", ticket.Error, "message", ticket.Message, "reference", ticket.Reference)
			}
		}
		if retried > 0 {
			// One line for a burst of them: a 503 from Apple answers every
			// stream of the batch alike.
			j.Log.Warn("pushes the service did not take now will be tried again", "via", call.route.String(), "pushes", retried, "first", firstRetry)
		}
	}
	// What was learned is written whatever else failed: a dead token must
	// not be pushed again, and a ticket must not go unread.
	closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := j.forget(closing, gone); err != nil {
		j.Log.Error("phones that are gone could not be forgotten", "error", err.Error())
	}
	if len(receiptIDs) > 0 {
		if err := q.RecordTickets(closing, RecordTicketsParams{TicketIds: receiptIDs, Tokens: receiptTokens, Now: now}); err != nil {
			j.Log.Error("the push tickets could not be kept; their receipts will not be read", "error", err.Error())
		} else {
			j.scheduleReceipts(closing)
		}
	}
	if len(failed) > 0 {
		return j.again(closing, nil, failed, attempt)
	}
	return nil
}

// sender is the one a route sends through. A route is only ever chosen for
// a sender that is set (messages leaves out the phones of one that is not).
func (j *Jobs) sender(r route) pushsend.Sender {
	if r == viaAPNs {
		return j.Sending.APNs
	}
	return j.Sending.Expo
}

// errPresence is a presence read that failed.
var errPresence = errors.New("delivery: presence could not be read")

// wantedRow is a row whose push may go, with what it says.
type wantedRow struct {
	id     int64
	userID string
	push   storedPush
	// collapseKey is the row's group ("" when it never collapses), which an
	// iPhone collapses the push by.
	collapseKey string
}

// storedPush is metadata.push as soloclef_notify/notify wrote it.
type storedPush struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Path  string `json:"path"`
}

// allowed keeps the rows whose push the owner allows now and who are not
// looking at Soloclef: the pure checks first, then one presence read for
// the owners that are left.
func (j *Jobs) allowed(ctx context.Context, rows []PushableRow, now time.Time) ([]wantedRow, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	userIDs := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if !seen[row.UserID] {
			seen[row.UserID] = true
			userIDs = append(userIDs, row.UserID)
		}
	}
	preferences, err := j.preferences(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	var kept []wantedRow
	for _, row := range rows {
		var stored storedPush
		if json.Unmarshal(row.Push, &stored) != nil || stored.Title == "" {
			j.Log.Warn("a row's push cannot be read; not pushed", "notification", row.ID)
			continue
		}
		verdict := pushpolicy.Decide(preferences[row.UserID], stored.Kind, now)
		if verdict != pushpolicy.Send {
			j.Log.Debug("not pushed", "notification", row.ID, "why", verdict.String())
			continue
		}
		kept = append(kept, wantedRow{id: row.ID, userID: row.UserID, push: stored, collapseKey: row.CollapseKey})
	}
	if len(kept) == 0 {
		return nil, nil
	}
	looking, err := j.Presence.Active(ctx, ownersOf(kept), now)
	if err != nil {
		// Not guessed either way: the rows go back for their next attempt,
		// as a push the service did not take does.
		return nil, fmt.Errorf("%w: %w", errPresence, err)
	}
	present := kept[:0]
	for _, row := range kept {
		if looking[row.userID] {
			j.Log.Debug("not pushed", "notification", row.id, "why", "looking at Soloclef")
			continue
		}
		present = append(present, row)
	}
	return present, nil
}

// preferences reads the owners' choices; an owner with no row has every
// kind on and no quiet hours. A stored zone that no longer loads (removed
// from the time zone database) drops the quiet hours and says so, rather
// than holding every push back.
func (j *Jobs) preferences(ctx context.Context, userIDs []string) (map[string]pushpolicy.Preferences, error) {
	rows, err := New(j.Database).Preferences(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("delivery: reading push choices: %w", err)
	}
	out := make(map[string]pushpolicy.Preferences, len(userIDs))
	for _, userID := range userIDs {
		out[userID] = pushpolicy.Defaults()
	}
	for _, row := range rows {
		// The one reading of a stored row (pushpolicy.FromStored), which the
		// App Store update check shares.
		preferences, err := pushpolicy.FromStored(pushpolicy.Stored{
			Choices: row.Choices, QuietStartMinute: row.QuietStartMinute, QuietEndMinute: row.QuietEndMinute, TimeZone: row.TimeZone,
		})
		if err != nil {
			j.Log.Warn("an account's quiet hours cannot be read; ignored", "user", row.UserID, "error", err.Error())
		}
		out[row.UserID] = preferences
	}
	return out, nil
}

// leftOut counts the phones a delivery could not offer to any sender.
type leftOut struct {
	// noAPNs are APNs tokens on a worker with no Apple key.
	noAPNs int
	// otherEnvironment are APNs tokens of the environment this worker does
	// not push.
	otherEnvironment int
	// unknownKind are rows of a kind this build does not know (a newer
	// API's); the schema allows none today.
	unknownKind int
}

// messages is each wanted row's push to each of its owner's phones (on a
// retry, the phones named for it), one outgoing per row and sender.
func (j *Jobs) messages(rows []wantedRow, devices []LiveDevicesRow, counts []UnreadCountsRow, only phones) ([]outgoing, leftOut) {
	byOwner := map[string][]LiveDevicesRow{}
	for _, device := range devices {
		byOwner[device.UserID] = append(byOwner[device.UserID], device)
	}
	badges := map[string]int{}
	for _, count := range counts {
		badges[count.UserID] = int(count.Unread)
	}
	var out []outgoing
	var left leftOut
	for _, row := range rows {
		badge := badges[row.userID]
		data := map[string]any{"kind": row.push.Kind, "path": row.push.Path, "v": payloadVersion, "server": j.Sending.Origin}
		message := pushsend.Message{
			Title: row.push.Title, Body: row.push.Body, Data: data, Sound: "default", Badge: &badge,
			ChannelID: androidChannel, ThreadID: row.push.Kind, CollapseID: row.collapseKey, TTL: pushTTL,
		}
		perRoute := map[route]*outgoing{}
		var order []route
		for _, device := range byOwner[row.userID] {
			if only != nil && !only[row.id][device.ID] {
				continue
			}
			r, ok := j.routeOf(device, &left)
			if !ok {
				continue
			}
			group := perRoute[r]
			if group == nil {
				group = &outgoing{notificationID: row.id, route: r}
				perRoute[r] = group
				order = append(order, r)
			}
			message.To = device.Token
			group.devices = append(group.devices, device.ID)
			group.messages = append(group.messages, message)
		}
		for _, r := range order {
			out = append(out, *perRoute[r])
		}
	}
	return out, left
}

// routeOf is the sender a phone is pushed by, or false (and counted) when
// no sender here may push it.
func (j *Jobs) routeOf(device LiveDevicesRow, left *leftOut) (route, bool) {
	switch device.Kind {
	case pushsend.KindExpo:
		return viaExpo, true
	case pushsend.KindAPNs:
		switch {
		case j.Sending.APNs == nil:
			left.noAPNs++
			return 0, false
		case device.ApnsEnvironment == nil || *device.ApnsEnvironment != j.Sending.APNsEnvironment:
			// Apple would refuse it as a bad token, and the token is good
			// where it came from: it is left alone, not sent and deleted.
			left.otherEnvironment++
			return 0, false
		}
		return viaAPNs, true
	}
	left.unknownKind++
	return 0, false
}

// logLeftOut says which phones were not pushed and why, once a delivery.
func (j *Jobs) logLeftOut(left leftOut) {
	if left.noAPNs > 0 {
		j.Log.Warn("iPhones registered with an Apple token were not pushed: this worker has no APNs key (SOLOCLEF_WORKER_APNS_*)", "phones", left.noAPNs)
	}
	if left.otherEnvironment > 0 {
		j.Log.Info("phones registered in the other APNs environment were left alone (a development build on this server, or the reverse)",
			"phones", left.otherEnvironment, "environment", j.Sending.APNsEnvironment)
	}
	if left.unknownKind > 0 {
		j.Log.Error("phones of a token kind this worker does not know were not pushed", "phones", left.unknownKind)
	}
}

// forget deletes the tokens of phones that are gone.
func (j *Jobs) forget(ctx context.Context, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}
	removed, err := New(j.Database).ForgetDevices(ctx, tokens)
	if err == nil {
		j.Log.Info("phones the push service says are gone were forgotten", "phones", removed)
	}
	return err
}

// again republishes the rows for their next attempt, after a wait that
// doubles each time, or gives up on them after the last: to every live
// phone of the rows when only is nil, otherwise to exactly the phones only
// names.
func (j *Jobs) again(ctx context.Context, ids []int64, only phones, attempt int) error {
	next := attempt + 1
	if next >= j.Timing.Attempts {
		j.Log.Error("pushes were given up on after every attempt; the rows are in the inbox", "rows", ids, "phones", only.lists(), "attempts", next)
		return nil
	}
	wait := time.Duration(float64(j.Timing.Backoff) * math.Pow(2, float64(attempt)))
	var message produce.Message
	if only == nil {
		message = notify.DeliverMessage(ids, next, wait)
	} else {
		message = notify.RetryMessage(only.lists(), next, wait)
	}
	if _, err := j.Publisher.Send(ctx, message); err != nil {
		return fmt.Errorf("delivery: the retry of rows %v could not be queued: %w", message.Args[0], err)
	}
	return nil
}

// scheduleReceipts queues the receipts check, once per ReceiptDelay
// however many deliveries ask: one check reads every ticket that is due.
func (j *Jobs) scheduleReceipts(ctx context.Context) {
	won, err := j.Fence.Take(ctx, receiptsFence, j.Timing.ReceiptDelay)
	if err != nil {
		j.Log.Warn("the receipts check could not be fenced; scheduling it anyway", "error", err.Error())
	} else if !won {
		return
	}
	if _, err := j.Publisher.Send(ctx, notify.ReceiptsMessage(j.Timing.ReceiptDelay)); err != nil {
		j.Log.Warn("the receipts check could not be queued; the next delivery will", "error", err.Error())
	}
}

func ownersOf(rows []wantedRow) []string {
	seen := map[string]bool{}
	var owners []string
	for _, row := range rows {
		if !seen[row.userID] {
			seen[row.userID] = true
			owners = append(owners, row.userID)
		}
	}
	return owners
}

// distinct is the ids once each, in their first order: a batch's rows for
// the log, where a row with two phones appears twice.
func distinct(ids []int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// phones is a retry's phones (push_devices ids) for each row. nil, on a
// first delivery, is every live phone of each row's owner.
type phones map[int64]map[int64]bool

func (p phones) add(row, device int64) {
	if p[row] == nil {
		p[row] = map[int64]bool{}
	}
	p[row][device] = true
}

// lists is the phones in the form notify.RetryMessage takes, each row's in
// order.
func (p phones) lists() map[int64][]int64 {
	if p == nil {
		return nil
	}
	out := make(map[int64][]int64, len(p))
	for row, set := range p {
		list := make([]int64, 0, len(set))
		for device := range set {
			list = append(list, device)
		}
		slices.Sort(list)
		out[row] = list
	}
	return out
}

// idsOf reads the one argument, a list of row ids. JSON has one number
// type, so each id arrives as a float64 and is accepted only when whole and
// positive.
func idsOf(msg *celery.Message) ([]int64, error) {
	if len(msg.Args) != 1 {
		return nil, fmt.Errorf("delivery: %s takes one argument, the row ids; got %d", msg.Task, len(msg.Args))
	}
	list, ok := msg.Args[0].([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("delivery: %v is not a list of row ids", msg.Args[0])
	}
	ids := make([]int64, len(list))
	for i, raw := range list {
		id, ok := wholeID(raw)
		if !ok {
			return nil, fmt.Errorf("delivery: %v is not a row id", raw)
		}
		ids[i] = id
	}
	return ids, nil
}

// phonesOf reads a retry's phones (notify.DevicesKwarg), or nil when the
// message names none. A row it names must be one of the message's rows: a
// retry that names others is not the job's own and is refused.
func phonesOf(msg *celery.Message, ids []int64) (phones, error) {
	raw, named := msg.Kwargs[notify.DevicesKwarg]
	if !named {
		return nil, nil
	}
	byRow, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("delivery: %s is not a map of rows to phones", notify.DevicesKwarg)
	}
	if len(byRow) == 0 {
		return nil, fmt.Errorf("delivery: %s names no rows; a retry always names the phones it is for", notify.DevicesKwarg)
	}
	out := phones{}
	for key, value := range byRow {
		row, err := strconv.ParseInt(key, 10, 64)
		if err != nil || !slices.Contains(ids, row) {
			return nil, fmt.Errorf("delivery: %s names %q, which is not one of the rows %v", notify.DevicesKwarg, key, ids)
		}
		list, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("delivery: the phones of row %d are not a list", row)
		}
		for _, item := range list {
			device, ok := wholeID(item)
			if !ok {
				return nil, fmt.Errorf("delivery: %v is not a phone's id", item)
			}
			out.add(row, device)
		}
	}
	return out, nil
}

func wholeID(raw any) (int64, bool) {
	number, ok := raw.(float64)
	if !ok || number < 1 || number > math.MaxInt64/2 || number != math.Trunc(number) {
		return 0, false
	}
	return int64(number), true
}
