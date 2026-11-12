// Package notebookname is the job that names a notebook from its sources,
// or from its first question when it has no source ready yet.
//
// # The message
//
// `notebook_autoname(notebook_id, trigger, question)`, as the API tier's
// chat publishes it (internal/chat/mentions.go): the trigger is "source" or
// "question", and the question is the first one asked, or null. Whose
// notebook it is, is read off the row, and every write names that owner.
//
// The job is a courtesy. A notebook that could not be named keeps its
// title; nothing waits on this, and a failure is logged and the message
// acknowledged, never retried.
package notebookname

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/llm"
)

// Task is the message's name.
const Task = "notebook_autoname"

// Job runs notebook_autoname messages.
type Job struct {
	Rows Rows
	// Model is the quick model, with its reasoning off.
	Model       model.Model
	CallTimeout time.Duration
	Log         *slog.Logger
	// Now is the clock the refinement window is read against.
	Now func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (j *Job) Check() error {
	if j.Rows == nil || j.Model == nil || j.Log == nil || j.Now == nil || j.CallTimeout <= 0 {
		return errors.New("notebookname: a dependency is missing")
	}
	return nil
}

// Run is the jobs.Handler.
func (j *Job) Run(ctx context.Context, msg *celery.Message) error {
	notebookID, trigger, question, err := arguments(msg)
	if err != nil {
		return err
	}
	notebook, err := j.Rows.Notebook(ctx, notebookID)
	if errors.Is(err, ErrGone) {
		return nil
	}
	if err != nil {
		return err
	}
	current := readState(notebook.TitleSource, notebook.AutoTitle)
	if current.titleSource != "auto" || current.runs >= maxRuns {
		return nil
	}
	sources, err := j.Rows.ReadySources(ctx, notebook)
	if err != nil {
		return err
	}
	now := j.Now()
	basis := decide(current, sources.Ready, question != "", trigger, now)
	if basis == "" {
		return nil
	}

	var material, language string
	if basis == fromSources {
		material = sourcesMaterial(sources.First)
		var sample []string
		for _, source := range sources.First {
			sample = append(sample, firstRunes(squash(source.text), excerptRunes))
		}
		language = targetLanguage(strings.Join(sample, " "))
		if strings.TrimSpace(strings.Join(sample, "")) == "" {
			var titles []string
			for _, source := range sources.First {
				titles = append(titles, source.title)
			}
			language = targetLanguage(strings.Join(titles, " "))
		}
	} else {
		material = questionMaterial(question)
		language = targetLanguage(question)
	}

	named, err := j.suggest(ctx, material, language)
	if err != nil {
		return err
	}
	if named.title == "" {
		j.Log.Info("the model gave no usable name; the notebook keeps its title", "notebook", notebook.ID)
		return nil
	}
	renamed, err := j.Rows.Rename(ctx, notebook, Naming{
		Title: named.title, Icon: named.icon, AutoTitle: next(current.runs, basis, sources.Ready, now),
		ExpectedRuns: current.runs, OldTitle: notebook.Title,
	})
	if err != nil {
		return err
	}
	if !renamed {
		j.Log.Info("the notebook was renamed meanwhile; the automatic name is dropped", "notebook", notebook.ID)
		return nil
	}
	j.Log.Info("notebook named", "notebook", notebook.ID, "from", string(basis), "run", current.runs+1)
	return nil
}

// suggest asks for a name in the language, and once more if the reply came
// back in another one. A second miss is not accepted.
func (j *Job) suggest(ctx context.Context, material, language string) (suggestion, error) {
	instruction := prompt + " " + languageInstruction(language)
	for _, extra := range []string{"", retryInstruction(language)} {
		system := instruction
		if extra != "" {
			system += "\n\n" + extra
		}
		reply, err := llm.Ask(ctx, j.Model, j.CallTimeout, []model.Message{
			{Role: model.RoleSystem, Content: system},
			{Role: model.RoleUser, Content: material},
		})
		if err != nil {
			return suggestion{}, fmt.Errorf("asking for a notebook's name: %w", err)
		}
		named := parseReply(reply, language)
		if inLanguage(named.title, language) {
			return named, nil
		}
	}
	j.Log.Info("the model named the notebook in another language twice; the name is not used", "language", language)
	return suggestion{}, nil
}

func arguments(msg *celery.Message) (int64, Trigger, string, error) {
	if len(msg.Kwargs) != 0 || len(msg.Args) < 1 || len(msg.Args) > 3 {
		return 0, "", "", fmt.Errorf("notebookname: %s takes (notebook_id, trigger, question); got %d positional and %d keyword",
			Task, len(msg.Args), len(msg.Kwargs))
	}
	number, isNumber := msg.Args[0].(float64)
	if !isNumber || number < 1 || number != math.Trunc(number) || number > math.MaxInt64/2 {
		return 0, "", "", fmt.Errorf("notebookname: %v is not a notebook id", msg.Args[0])
	}
	trigger := triggerSource
	if len(msg.Args) > 1 {
		written, isText := msg.Args[1].(string)
		if !isText || (Trigger(written) != triggerSource && Trigger(written) != triggerQuestion) {
			return 0, "", "", fmt.Errorf("notebookname: %v is not a trigger (source or question)", msg.Args[1])
		}
		trigger = Trigger(written)
	}
	question := ""
	if len(msg.Args) > 2 && msg.Args[2] != nil {
		written, isText := msg.Args[2].(string)
		if !isText {
			return 0, "", "", fmt.Errorf("notebookname: the question is %T, not text", msg.Args[2])
		}
		question = strings.TrimSpace(written)
	}
	return int64(number), trigger, question, nil
}
