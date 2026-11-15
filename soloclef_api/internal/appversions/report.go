// Package appversions says which versions of the phone app are still in
// use: what the phones registered for push report, and what the App Store
// offers. It is how an operator decides that no installed build needs a
// snapshot of the server's contract any more (internal/installedbuilds,
// README.md), through `soloclef-api app-versions` (cmd/api).
package appversions

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RetirementWindow is how long no phone may have reported a build before
// its snapshot and compatibility code may go. A phone registers for push on
// every start of the app, so a build no phone has started in 30 days is
// one its owners stopped opening or updated past; the window is a month so
// that a phone opened once a month still counts as in use.
const RetirementWindow = 30 * 24 * time.Hour

// unversioned is how a phone that reports no version is named: a bundle
// from before the app sent `app_version` (1.0.0 and 1.1.0 until their
// updates of 2026-11-14).
const unversioned = "(none: an older bundle)"

// unreported is how a phone that reports no update is named: the bundle
// built into the app, or one from before updates were reported. Every
// snapshot of a bundle from before then retires on this row
// (internal/installedbuilds/README.md).
const unreported = "(none: built in, or an older bundle)"

// orSaid is the value, or what its absence means.
func orSaid(value *string, absent string) string {
	if value == nil {
		return absent
	}
	return *value
}

// Write prints the report: the store's versions, then each platform and
// version the phones report, with how many were seen within the window.
func Write(ctx context.Context, pool *pgxpool.Pool, now time.Time, output io.Writer) error {
	queries := New(pool)
	store, err := queries.StoreVersions(ctx)
	if err != nil {
		return fmt.Errorf("reading the App Store's versions: %w", err)
	}
	since := now.Add(-RetirementWindow)
	reported, err := queries.ReportedBundles(ctx, since)
	if err != nil {
		return fmt.Errorf("reading the bundles phones report: %w", err)
	}
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if len(store) == 0 {
		fmt.Fprintln(table, "App Store\t(not checked yet: the worker's daily check has not answered)")
	}
	for _, row := range store {
		fmt.Fprintf(table, "App Store\t%s offers %s (checked %s)\n", row.BundleID, row.Version, row.CheckedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintln(table)
	fmt.Fprintf(table, "PLATFORM\tAPP VERSION\tRUNTIME\tUPDATE\tSEEN SINCE %s\tREGISTERED\tLAST SEEN\n", since.UTC().Format("2006-01-02"))
	for _, row := range reported {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n", row.Platform, orSaid(row.AppVersion, unversioned),
			orSaid(row.RuntimeVersion, "-"), orSaid(row.UpdateID, unreported), row.SeenSince, row.Registered,
			row.LastSeenAt.UTC().Format(time.RFC3339))
	}
	return table.Flush()
}
