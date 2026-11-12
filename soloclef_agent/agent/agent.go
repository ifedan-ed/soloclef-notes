// Package agent is the tool-calling loop: call the model, run the tools it
// asked for, show it the results, repeat until it answers.
//
// That loop is all a chat needs from an "agent framework", and this package
// is that loop written out, with the four things that make it usable in
// production built in rather than bolted on: a stream of events, a pause for
// human approval that survives a restart, a checkpoint after every step, and
// agents that call other agents.
//
// Behaviour around the loop's two actions is a hook: a function handed the
// model call or the tool call and the next step, stacked in the order of a
// slice (see hooks.go). That replaces the Python chat's twenty-six numbered
// middleware slots. There is no compile step: New checks a handful of fields
// and builds two small maps, so an agent is cheap enough to build per turn
// from that turn's own prompt, tools and permissions, and nothing about a
// user needs caching between turns. BenchmarkNew measures it.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// Config is everything an agent is. The fields without "optional" in their
// comment must be set: New refuses a config that leaves one out, because each
// is a decision (which model, how many steps, where state lives) that no
// default could make correctly for a caller it has never seen.
type Config struct {
	// Model is the one model this agent calls.
	Model model.Model

	// System is the system prompt. It is sent at the head of every model call
	// and never stored in the thread, so changing it takes effect on the next
	// call and old threads carry no stale copy. Optional: an agent may have
	// none.
	System string

	// Tools are the tools the model may call.
	Tools []tool.Tool

	// Delegates are other agents the model may hand a task to. Optional.
	Delegates []Delegate

	// MaxSteps is how many steps one turn may take, a step being one model
	// call or one batch of tool calls. It is counted per turn, from the
	// message that starts the turn, across any pauses for approval. It is
	// never counted across turns: a thread does not wear out.
	//
	// It is the guard against a model that calls tools forever. A turn that
	// reaches it ends with ErrStepLimit, its progress saved.
	MaxSteps int

	// MaxParallelTools is how many tool calls of one batch may run at the
	// same time. It applies only to tools marked Parallel; 1 runs everything
	// one at a time.
	MaxParallelTools int

	// Store is where the thread's state is saved after every step.
	Store checkpoint.Store

	// Trusted is asked, for a call to a tool whose Approval is tool.Ask,
	// whether this call may run without asking. Optional: with none, every
	// such call asks.
	//
	// It is called at the moment the call is decided, every time, and its
	// answer is not remembered. So a trust list it reads from a database row
	// stops applying the moment the row changes, with nothing to invalidate.
	// It is never called for a tool.AlwaysAsk tool: that policy is not
	// something trust can waive.
	Trusted func(ctx context.Context, call tool.Call) (bool, error)

	// Prepare builds the messages for a model call from the stored
	// conversation. Optional: with none, the stored conversation is sent as
	// it is.
	//
	// Whatever it returns is sent and not stored. This is the place for
	// anything the model should see on every call without it accumulating in
	// the thread: a memory block, the documents attached to this turn, a
	// trimmed view of old tool results. It must not modify the slice it is
	// given.
	Prepare func(ctx context.Context, stored []model.Message) ([]model.Message, error)

	// ModelHooks are put around every model call, the first outermost.
	// Optional. See ModelHook.
	ModelHooks []ModelHook

	// ToolHooks are put around every tool call and every delegate call, the
	// first outermost. Optional. They are not inherited by delegates: a
	// delegate's agent has its own, so a hook meant for the whole tree is
	// given to each agent that should run it.
	ToolHooks []ToolHook
}

// Delegate offers another agent to the model as a tool that takes a task and
// returns that agent's answer.
type Delegate struct {
	// Name and Description are what the model sees, as for a tool.
	Name        string
	Description string

	// Agent runs the task, on a thread of its own named after the parent's
	// thread and the call. It starts with an empty conversation and the task
	// as its first message: it sees nothing of the parent's conversation
	// that the parent's model did not put in the task.
	Agent *Agent

	// Parallel has the meaning it has on a tool.
	Parallel bool

	// Timeout is how long one stretch of the delegate's work may take: from
	// the call starting, or from a resume, until its agent answers or
	// pauses. Time spent waiting for a person is not counted, because no
	// work is running then. Optional: zero sets no limit of its own, and
	// the delegate is bounded by its agent's MaxSteps and the run's context
	// alone.
	//
	// A delegate that passes it is one failed tool result, which the
	// calling model reads and acts on, and its half-finished thread is
	// removed. It is a field here and not something for a tool hook to add
	// because of that removal: a hook can cut the call short but cannot
	// know there is a thread to clean up behind it.
	Timeout time.Duration
}

// delegateParameters is the schema every delegate presents. It is fixed
// because the hand-off is fixed: a delegate is given a task in words and
// nothing else, so there is no per-delegate schema for a model to get wrong.
var delegateParameters = json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","description":"What to do, with everything needed to do it. The recipient sees only this text."}},"required":["task"]}`)

// Agent is a built, validated Config. It holds no per-thread or per-user
// state, so one Agent may run any number of threads at once.
type Agent struct {
	cfg       Config
	tools     map[string]*tool.Tool
	delegates map[string]*Delegate

	// specs is the tool list as the model is shown it, built once here
	// because it is identical for every call the agent makes.
	specs []model.ToolSpec

	// names is the tool list in declaration order, for the message that
	// answers a call to a tool that does not exist.
	names []string

	// generate is the model with the model hooks stacked over it.
	generate ModelFunc
}

// New checks cfg and returns the agent. Every mistake it can catch is caught
// here, so that a misconfigured agent fails when the program builds it and
// not partway through somebody's conversation.
func New(cfg Config) (*Agent, error) {
	switch {
	case cfg.Model == nil:
		return nil, errors.New("agent: Model is not set")
	case cfg.Store == nil:
		return nil, errors.New("agent: Store is not set; use checkpoint.NewMemory() for state that need not outlive the process")
	case cfg.MaxSteps < 1:
		return nil, fmt.Errorf("agent: MaxSteps must be at least 1, got %d", cfg.MaxSteps)
	case cfg.MaxParallelTools < 1:
		return nil, fmt.Errorf("agent: MaxParallelTools must be at least 1, got %d", cfg.MaxParallelTools)
	}

	for i, h := range cfg.ModelHooks {
		if h == nil {
			return nil, fmt.Errorf("agent: model hook %d is nil", i)
		}
	}
	for i, h := range cfg.ToolHooks {
		if h == nil {
			return nil, fmt.Errorf("agent: tool hook %d is nil", i)
		}
	}

	a := &Agent{
		cfg:       cfg,
		tools:     make(map[string]*tool.Tool, len(cfg.Tools)),
		delegates: make(map[string]*Delegate, len(cfg.Delegates)),
		specs:     make([]model.ToolSpec, 0, len(cfg.Tools)+len(cfg.Delegates)),
		names:     make([]string, 0, len(cfg.Tools)+len(cfg.Delegates)),
		generate:  chainModel(cfg.Model, cfg.ModelHooks),
	}
	taken := func(name string) bool {
		return a.tools[name] != nil || a.delegates[name] != nil
	}
	for i := range cfg.Tools {
		t := &cfg.Tools[i]
		if err := t.Validate(); err != nil {
			return nil, fmt.Errorf("agent: %w", err)
		}
		if taken(t.Name) {
			return nil, fmt.Errorf("agent: two tools are named %q", t.Name)
		}
		a.tools[t.Name] = t
		a.names = append(a.names, t.Name)
		a.specs = append(a.specs, model.ToolSpec{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	for i := range cfg.Delegates {
		d := &cfg.Delegates[i]
		if !tool.ValidName(d.Name) {
			return nil, fmt.Errorf("agent: delegate name %q must be 1 to 64 characters of letters, digits, underscore and hyphen", d.Name)
		}
		if d.Agent == nil {
			return nil, fmt.Errorf("agent: delegate %s has no Agent", d.Name)
		}
		if d.Timeout < 0 {
			return nil, fmt.Errorf("agent: delegate %s has a negative Timeout, %s", d.Name, d.Timeout)
		}
		if taken(d.Name) {
			return nil, fmt.Errorf("agent: two tools or delegates are named %q", d.Name)
		}
		a.delegates[d.Name] = d
		a.names = append(a.names, d.Name)
		a.specs = append(a.specs, model.ToolSpec{Name: d.Name, Description: d.Description, Parameters: delegateParameters})
	}
	return a, nil
}
