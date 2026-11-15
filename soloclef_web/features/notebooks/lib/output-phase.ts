/**
 * Phases a Studio generation reports while it runs (artifact and video
 * deliverable jobs, and podcast runs). Anything else falls back to the
 * generic status label.
 */
export const KNOWN_PHASES = [
	"preparing",
	"drafting",
	"authoring",
	"narrating",
	"reviewing",
	"repairing",
	"rendering",
	"verifying",
	"saving",
	// An artifact whose account has every sandbox in use waits for one
	// (soloclef_assistant/deliverables/sandboxwait.go).
	"waiting_for_sandbox",
] as const;
export type KnownPhase = (typeof KNOWN_PHASES)[number];

export function knownPhase(phase: string | null | undefined): KnownPhase | null {
	return phase && (KNOWN_PHASES as readonly string[]).includes(phase)
		? (phase as KnownPhase)
		: null;
}
