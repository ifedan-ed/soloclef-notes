package activitylogs

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// ListPath is the task log list. It is the collection the create posts to.
const ListPath = CreatePath

// The page of the list. The Python route took any limit, so one request
// could ask for the whole table; a page here is at most maxPageSize entries
// and a client that wants more asks for the next page.
const (
	defaultPageSize = 100
	maxPageSize     = 500
)

// Filter is what a list request asked for. A nil field was not asked.
type Filter struct {
	Level, Status *string
	// Source is the text a person typed, matched anywhere in the source
	// and never as a pattern (Store.List escapes it).
	Source              *string
	NotBefore, NotAfter *time.Time
	Skip, PageSize      int32
}

// Listing reads a page of the account's log (Store).
type Listing interface {
	List(ctx context.Context, ownerID string, filter Filter) ([]Entry, error)
}

// List is a page of the owner's entries, newest first.
func (s Store) List(ctx context.Context, ownerID string, filter Filter) ([]Entry, error) {
	params := LogsOfOwnerParams{
		UserID: ownerID, NotBefore: filter.NotBefore, NotAfter: filter.NotAfter,
		Skip: filter.Skip, PageSize: filter.PageSize,
	}
	if filter.Level != nil {
		params.Level = NullLoglevel{Loglevel: Loglevel(*filter.Level), Valid: true}
	}
	if filter.Status != nil {
		params.Status = NullLogstatus{Logstatus: Logstatus(*filter.Status), Valid: true}
	}
	if filter.Source != nil {
		// ILIKE reads % and _ as wildcards and \ as its escape. A source
		// such as "nextcloud_import" must find that and not "nextcloudximport".
		pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(*filter.Source) + "%"
		params.SourcePattern = &pattern
	}
	rows, err := New(s.Pool).LogsOfOwner(ctx, params)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, Entry{
			CreatedAt: row.CreatedAt, ID: row.ID, Level: row.Level, Status: row.Status,
			Message: row.Message, Source: row.Source, Metadata: row.LogMetadata,
		})
	}
	return entries, nil
}

// ListRoute serves the list.
type ListRoute struct {
	Logs   Listing
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ListRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-logs",
		Method:      http.MethodGet,
		Path:        ListPath,
		Summary:     "The task log",
		Description: "The caller's own activity log, newest first, filtered " +
			"by level, status, source and time, a page at a time.",
		Tags: []string{"logs"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ListRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// read if the wiring is ever wrong.
		logFailure(ctx, r.Logger, requestID, "the route ran without an authenticated caller", nil, "the log list could not be served")
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	address := ctx.URL()
	filter, failures := readFilter(address.Query())
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}
	entries, err := r.Logs.List(ctx.Context(), caller.UserID, filter)
	if err != nil {
		logFailure(ctx, r.Logger, requestID, "could not read the log list", err, "the log list could not be served")
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	rendered := make([]any, 0, len(entries))
	for _, entry := range entries {
		one, err := Build(entry)
		if err != nil {
			// One entry whose metadata is not an object used to fail the
			// whole list, which hid every other entry behind it. It is
			// listed without its metadata, and the line below says which.
			r.Logger.WarnContext(ctx.Context(), "a log entry's metadata could not be rendered; listed without it",
				slog.String("request_id", requestID), slog.Int64("log_id", entry.ID), slog.String("error", err.Error()))
			entry.Metadata = nil
			if one, err = Build(entry); err != nil {
				continue
			}
		}
		rendered = append(rendered, one)
	}
	body, err := pythonjson.Marshal(rendered)
	if err != nil {
		logFailure(ctx, r.Logger, requestID, "could not render the log list", err, "the log list could not be served")
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// readFilter reads the query. Every value that cannot be read is reported,
// together, so a form shows all of its mistakes at once. An empty value is
// "not asked": the filter form sends its untouched fields that way.
func readFilter(query url.Values) (Filter, []errorenvelope.Failure) {
	filter := Filter{PageSize: defaultPageSize}
	var failures []errorenvelope.Failure
	refuse := func(name, message string) {
		failures = append(failures, errorenvelope.Failure{Location: []string{"query", name}, Message: message})
	}
	asked := func(name string) (string, bool) {
		value := strings.TrimSpace(query.Get(name))
		return value, value != ""
	}
	if text, ok := asked("skip"); ok {
		skip, err := strconv.ParseInt(text, 10, 32)
		if err != nil || skip < 0 {
			refuse("skip", "Input should be a whole number of 0 or more")
		}
		filter.Skip = int32(skip)
	}
	if text, ok := asked("limit"); ok {
		limit, err := strconv.ParseInt(text, 10, 32)
		if err != nil || limit < 1 || limit > maxPageSize {
			refuse("limit", "Input should be a whole number from 1 to "+strconv.Itoa(maxPageSize))
		}
		filter.PageSize = int32(limit)
	}
	if text, ok := asked("level"); ok {
		if !slices.Contains(levels, text) {
			refuse("level", "Input should be one of "+strings.Join(levels, ", "))
		}
		filter.Level = &text
	}
	if text, ok := asked("status"); ok {
		if !slices.Contains(statuses, text) {
			refuse("status", "Input should be one of "+strings.Join(statuses, ", "))
		}
		filter.Status = &text
	}
	if text, ok := asked("source"); ok {
		filter.Source = &text
	}
	for _, bound := range []struct {
		name string
		into **time.Time
		// A day with no time means the whole day, so as an upper bound it
		// is the day's last instant: "until the 11th" includes the 11th.
		endOfDay bool
	}{{"start_date", &filter.NotBefore, false}, {"end_date", &filter.NotAfter, true}} {
		text, ok := asked(bound.name)
		if !ok {
			continue
		}
		moment, dayOnly, err := readMoment(text)
		if err != nil {
			refuse(bound.name, "Input should be a date (2026-11-12) or a date and time (2026-11-12T09:30:00Z)")
			continue
		}
		if dayOnly && bound.endOfDay {
			moment = moment.Add(24*time.Hour - time.Microsecond)
		}
		*bound.into = &moment
	}
	return filter, failures
}

// readMoment reads a filter's date. A time with no offset is UTC, which is
// what the entries are stamped in.
func readMoment(text string) (moment time.Time, dayOnly bool, err error) {
	if moment, err = time.Parse(time.RFC3339Nano, text); err == nil {
		return moment, false, nil
	}
	if moment, err = time.Parse("2006-01-02T15:04:05.999999999", text); err == nil {
		return moment, false, nil
	}
	moment, err = time.Parse(time.DateOnly, text)
	return moment, true, err
}
