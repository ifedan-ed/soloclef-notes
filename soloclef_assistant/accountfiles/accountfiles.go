// Package accountfiles reads a file for one account's chat turn: the
// shared reader (soloclef_ingest/fileread) with that account's choices
// filled in. The mail, Drive and Nextcloud tools all read through it, so an
// attachment, a Drive file and a Nextcloud file are read by the same
// parsers, under the same bounds, and described by the same vision model.
//
// # Which vision model
//
// The account's, by the rule every picture follows (soloclef_assistant/
// accountvision): its own choice, a proxy name or a model of its own
// connection, else the deployment's default (CHAT_VISION_MODEL), else
// none, which the result says. Nothing else is tried.
//
// # Which parser
//
// The one the connection chose (its row's `parser` column), which the
// caller reads with its connection and passes in; none is the deployment's
// default, as for an upload.
package accountfiles

import (
	"context"
	"errors"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/accountvision"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

// Reader reads files for an account.
type Reader struct {
	Files *fileread.Reader
	// Vision is the account's vision model (accountvision.ForDeployment).
	Vision accountvision.Models
}

// Check reports what is missing. Called once at start-up.
func (r *Reader) Check() error {
	if r.Files == nil {
		return errors.New("accountfiles: the file reader is required")
	}
	return r.Vision.Check()
}

// Read reads one file for the account, with the parser its connection
// chose ("" for the deployment's default).
func (r *Reader) Read(ctx context.Context, userID, connectionParser string, file fileread.File) (fileread.Text, error) {
	// Which model is read now, for every file, so a choice that cannot be
	// read fails the read rather than leaving the picture to the
	// deployment's model; the model itself is opened only for a picture.
	name, err := r.Vision.Chosen(ctx, userID)
	if err != nil {
		return fileread.Text{}, err
	}
	choices := fileread.Choices{Parser: connectionParser}
	if name != "" {
		choices.Vision = func(ctx context.Context) (vision.Describer, error) {
			opened, err := r.Vision.Open.Open(ctx, userID, name)
			if sentence, said := accountvision.Sentence(err); said {
				return nil, &fileread.Unreadable{Reason: sentence}
			}
			return opened, err
		}
	}
	return r.Files.Read(ctx, file, choices)
}
