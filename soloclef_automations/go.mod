// What the two tiers that deal in automations must agree on: what a saved
// definition may say (definition), when a schedule next fires (schedule),
// whether an event matches a trigger's filter (eventfilter), and the
// messages that start a run (runqueue).
//
// A module of its own because the API tier writes an automation and the
// worker runs it, and `internal` cannot cross a module. The API refuses at
// save time exactly what the worker would refuse at run time, because both
// ask this code; a second copy would be a definition one tier saves and the
// other cannot run.
module git.danvics.com/danvics/soloclef/soloclef_automations

go 1.24.4

require (
	git.danvics.com/danvics/soloclef/soloclef_celery v0.0.0
	github.com/jackc/pgx/v5 v5.7.1
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	golang.org/x/crypto v0.27.0 // indirect
	golang.org/x/text v0.18.0 // indirect
)

replace git.danvics.com/danvics/soloclef/soloclef_celery => ../soloclef_celery
