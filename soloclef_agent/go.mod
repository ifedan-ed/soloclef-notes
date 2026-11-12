// An agent-orchestration library in Go: the tool-calling loop Soloclef's chat
// runs on, with streaming events, human approval, checkpoints and nested
// agents, plus a small typed state graph for fixed pipelines.
//
// It is a module of its own, required with a relative replace by whichever
// tier runs chat, for the same reason soloclef_chunker is one: more than one
// tier will run agents, and `internal` cannot be shared across modules.
//
// Its one dependency is pgx, and only the checkpoint/postgres package imports
// it. Everything else is the standard library, so a program that keeps its
// checkpoints elsewhere links no database driver.
module git.danvics.com/danvics/soloclef/soloclef_agent

go 1.24.4

require github.com/jackc/pgx/v5 v5.7.1

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	golang.org/x/crypto v0.27.0 // indirect
	golang.org/x/text v0.18.0 // indirect
)
