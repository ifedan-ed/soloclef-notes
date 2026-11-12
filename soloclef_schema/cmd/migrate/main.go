// Command migrate brings a Soloclef database to the schema this binary was
// built with, and says where a database stands.
//
//	migrate up                 apply every migration the database lacks
//	migrate status             list the migrations and when each was applied
//	migrate down-to <version>  roll back; development databases only
//
// It is the only thing that changes the schema. The API, the indexer and
// the worker check the version at start-up and refuse a database that is
// not at theirs (schemaversion); none of them migrates. So a deployment
// runs `migrate up` first, as its own step, and starts the services after
// it has exited 0: docker/docker-compose.go.yml does exactly that.
//
// The migrations are compiled in. The settings are in settings.go.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(arguments []string) error {
	// The command line is checked before anything is connected to, so a
	// typing mistake costs nothing.
	if len(arguments) == 0 {
		return errUsage
	}
	command := arguments[0]
	var target int64
	switch {
	case (command == "up" || command == "status") && len(arguments) == 1:
	case command == "down-to" && len(arguments) == 2:
		version, err := strconv.ParseInt(arguments[1], 10, 64)
		if err != nil || version < 0 {
			return fmt.Errorf("%q is not a version\n%w", arguments[1], errUsage)
		}
		target = version
	default:
		return errUsage
	}

	// A migration interrupted by SIGTERM is cancelled inside its
	// transaction, so the file is applied whole or not at all.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	address, err := databaseURL(os.Getenv, readFile)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		// pgx's parse error repeats the URL, password included.
		return fmt.Errorf("%s is not a Postgres address this driver can read", DatabaseURL)
	}
	defer pool.Close()
	var database string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return fmt.Errorf("the database is unreachable: %w", err)
	}

	switch command {
	case "up":
		applied, err := schema.Migrate(ctx, pool)
		for _, migration := range applied {
			fmt.Printf("applied %s\n", migration.Name)
		}
		if err != nil {
			return err
		}
		if len(applied) == 0 {
			fmt.Printf("%s is already at the newest version; nothing was applied\n", database)
		}
		return nil
	case "status":
		migrations, err := schema.Status(ctx, pool)
		if err != nil {
			return err
		}
		pending := 0
		for _, migration := range migrations {
			if migration.Pending() {
				pending++
				fmt.Printf("pending  %s\n", migration.Name)
				continue
			}
			fmt.Printf("applied  %s  %s\n", migration.Name, migration.AppliedAt.UTC().Format("2006-01-02 15:04:05Z"))
		}
		fmt.Printf("%s: %d of %d migrations applied\n", database, len(migrations)-pending, len(migrations))
		return nil
	default: // down-to; the switch above admitted nothing else.
		if err := allowDown(os.Getenv, database); err != nil {
			return err
		}
		rolled, err := schema.DownTo(ctx, pool, target)
		for _, migration := range rolled {
			fmt.Printf("rolled back %s\n", migration.Name)
		}
		if err != nil {
			return err
		}
		if len(rolled) == 0 {
			fmt.Printf("%s is already at version %d or below; nothing was rolled back\n", database, target)
		}
		return nil
	}
}
