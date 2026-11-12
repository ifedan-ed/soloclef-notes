package chat

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/knowledgebase"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
)

// The knowledge-base specialist is offered in every chat, a notebook's
// included (the web and the phone chat only in notebooks), and not for a
// message with a source picked, which asked for that source. Where it is
// offered, the prompt says what it is for, and the step display has its
// line.
func TestTheKnowledgeBaseSpecialistIsOfferedUnlessASourceIsPicked(t *testing.T) {
	ask := func(data *rows, turn Turn) (tools []string, system string, built Built) {
		t.Helper()
		script := modeltest.NewScript(modeltest.Reply{Text: "ok"})
		agents, _, _, _ := production(t, script, script, data, checkpoint.NewMemory(), connected{websearch.Exa: true})
		// The specialist is only built here, never run: nothing reaches
		// the database.
		agents.KnowledgeBase = knowledgebase.Writer{Changes: knowledgebase.Changes{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
		built, err := agents.Build(context.Background(), turn)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := built.Agent.Start(context.Background(), "probe", []model.Message{model.User("x")}, event.Discard); err != nil {
			t.Fatal(err)
		}
		request := script.Requests()[0]
		for _, spec := range request.Tools {
			tools = append(tools, spec.Name)
		}
		sort.Strings(tools)
		return tools, request.Messages[0].Content, built
	}
	has := func(tools []string) bool {
		for _, name := range tools {
			if name == assembly.KnowledgeBaseName {
				return true
			}
		}
		return false
	}

	tools, system, built := ask(newRows(), Turn{ThreadID: 1, UserID: ada, Scope: "documents"})
	if !has(tools) || !strings.Contains(system, "<knowledge_base>\nTo change the user's documents and folders, hand the task to knowledge_base") {
		t.Errorf("a plain chat: %v\n%s", tools, system)
	}
	if built.Activities[assembly.KnowledgeBaseName].Active == "" || built.Activities["create_note"].Active == "" {
		t.Errorf("the step display has no line for the specialist or its tools: %+v", built.Activities)
	}

	if tools, system, _ := ask(newRows(), Turn{ThreadID: 1, UserID: ada, Scope: "all", PreferredTool: "exa"}); has(tools) || strings.Contains(system, "<knowledge_base>") {
		t.Errorf("a message with a source picked: %v", tools)
	}
	notebook := newRows()
	notebook.about.Notebook = &Notebook{ID: 5, Title: "Biology", DocumentIDs: []int64{12}}
	if tools, system, _ := ask(notebook, Turn{ThreadID: 1, UserID: ada, Scope: "documents"}); !has(tools) || !strings.Contains(system, "<knowledge_base>") {
		t.Errorf("a notebook's chat: %v", tools)
	}
}
