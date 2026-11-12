package main

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/config"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// fileStorageSettings hands the file store everything the deployment said
// about it: the configured backend, the directory, and the bucket.
//
// All of it, whichever backend is configured. This used to pass the backend
// and the directory only, so a deployment that named s3 reached
// internal/filestore with an empty bucket and could not start. And the
// directory is passed beside s3 (as the bucket is beside local) because a
// row is read from the backend it recorded, not from the configured one:
// internal/filestore opens each backend whose settings are here, and
// internal/config has already refused a set given by half.
func fileStorageSettings(settings config.Config) filestore.Settings {
	return filestore.Settings{
		Backend:   settings.FileStorageBackend,
		LocalRoot: settings.FileStorageLocalPath,
		S3: filestore.S3Settings{
			Endpoint:  settings.FileStorageS3Endpoint,
			Bucket:    settings.FileStorageS3Bucket,
			Region:    settings.FileStorageS3Region,
			PathStyle: settings.FileStorageS3PathStyle,
			AccessKey: settings.FileStorageS3AccessKey,
			SecretKey: settings.FileStorageS3SecretKey,
		},
	}
}
