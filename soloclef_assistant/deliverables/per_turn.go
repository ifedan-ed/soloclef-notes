package deliverables

import (
	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// PerTurn is the specialist as a turn's assembly is given it
// (assembly.ArtifactMaker): the deployment's specialist, run on each
// turn's own model and saving for that turn's account under its thread.
//
// It is here, with the specialist, because every tier that runs one gives
// it to an assembly the same way: the API for a chat's turn and the worker
// for an automation's step.
type PerTurn struct {
	// Specialist is everything but the model, which is the turn's.
	Specialist Specialist
}

// Delegate is the specialist for one turn.
func (p PerTurn) Delegate(chat model.Model, userID string, threadID int64) (agent.Delegate, error) {
	maker := p.Specialist
	maker.Model = chat
	return maker.Delegate(Owner{UserID: userID, ThreadID: threadID})
}

// Prompt is the body of the main agent's <artifacts> block.
func (PerTurn) Prompt() string { return ArtifactsPrompt() }

// Check reports what the specialist is missing, with a model in place.
func (p PerTurn) Check(chat model.Model) error {
	maker := p.Specialist
	maker.Model = chat
	return maker.Check()
}
