package main

import (
	"context"
	"io"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/appversions"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/config"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// appVersionsCommand prints which phone app versions are still in use:
//
//	docker exec <api container> soloclef-api app-versions
//
// It is what an operator reads before retiring an installed build's
// snapshot (internal/installedbuilds/README.md): a build may go once the
// App Store offers a newer version and no phone has reported it for
// appversions.RetirementWindow. A command for the reason set-admin is one:
// the image has no shell or SQL client to ask with.
const appVersionsCommand = "app-versions"

func appVersions(output io.Writer) error {
	settings, err := config.FromEnvironment()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, settings.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()
	return appversions.Write(ctx, pool, time.Now(), output)
}
