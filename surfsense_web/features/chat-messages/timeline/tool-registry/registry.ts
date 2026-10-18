"use client";

import dynamic from "next/dynamic";
import type { TimelineToolComponent } from "./types";

// Dynamic imports keep the per-tool UI bundles out of the main chunk —
// each component only loads when an assistant turn references it. Mirrors
// the existing ``components/assistant-ui/assistant-message.tsx`` pattern.
//
// Phase A note: the imported components are still typed as
// ``ToolCallMessagePartComponent`` from assistant-ui; the cast at the
// bottom of this file bridges the contract until the cutover commit
// retypes them to ``TimelineToolComponent``. The cast is a structural
// no-op — every consumed prop overlaps.

const UpdateMemoryToolUI = dynamic(
	() => import("@/components/tool-ui/user-memory").then((m) => ({ default: m.UpdateMemoryToolUI })),
	{ ssr: false }
);
const CreateAutomationToolUI = dynamic(
	() =>
		import("@/components/tool-ui/automation").then((m) => ({ default: m.CreateAutomationToolUI })),
	{ ssr: false }
);
const SandboxExecuteToolUI = dynamic(
	() =>
		import("@/components/tool-ui/sandbox-execute").then((m) => ({
			default: m.SandboxExecuteToolUI,
		})),
	{ ssr: false }
);
const VerifyArtifactToolUI = dynamic(
	() =>
		import("@/components/tool-ui/verify-artifact").then((m) => ({
			default: m.VerifyArtifactToolUI,
		})),
	{ ssr: false }
);
const CreateGoogleDriveFileToolUI = dynamic(
	() =>
		import("@/components/tool-ui/google-drive").then((m) => ({
			default: m.CreateGoogleDriveFileToolUI,
		})),
	{ ssr: false }
);
const DeleteGoogleDriveFileToolUI = dynamic(
	() =>
		import("@/components/tool-ui/google-drive").then((m) => ({
			default: m.DeleteGoogleDriveFileToolUI,
		})),
	{ ssr: false }
);

/**
 * Headers-only tools — the timeline shows their ``ItemHeader`` (title +
 * sub-bullets) but mounts no tool body beneath. Two reasons to use
 * this:
 *  - **Structural primitives** (``task``): the row IS the parent of a
 *    delegation span; its job is to label the group. Children render
 *    as their own indented entries.
 *  - **Suppressed connectors** (``link_preview``, ``multi_link_preview``,
 *    ``scrape_webpage``): citations they produce render inline in
 *    markdown; a separate card would be redundant noise.
 */
const NullApprovalBody: TimelineToolComponent = () => null;

/**
 * Tool-name → component map used only to render pending HITL actions.
 *
 * Includes only "process" tools (connector CRUD, sandbox execute,
 * memory updates) and the 4 invisible tools mapped to a null component.
 * Deliverables live in ``BODY_TOOLS`` in ``assistant-message.tsx`` —
 * they're product, not process.
 *
 * Tools NOT in this map fall through to ``FallbackToolBody`` (which
 * itself dispatches between HITL approval cards and
 * ``DefaultFallbackCard`` based on result discrimination).
 */
const TOOLS_BY_NAME = {
	task: NullApprovalBody,
	create_automation: CreateAutomationToolUI,
	update_memory: UpdateMemoryToolUI,
	execute: SandboxExecuteToolUI,
	execute_code: SandboxExecuteToolUI,
	verify_artifact: VerifyArtifactToolUI,
	create_google_drive_file: CreateGoogleDriveFileToolUI,
	delete_google_drive_file: DeleteGoogleDriveFileToolUI,
	link_preview: NullApprovalBody,
	multi_link_preview: NullApprovalBody,
	scrape_webpage: NullApprovalBody,
} as unknown as Record<string, TimelineToolComponent>;

/**
 * Lookup a tool component by name. Returns ``undefined`` for unknown
 * tools so the caller can mount ``FallbackToolBody`` instead.
 */
export function getToolComponent(toolName: string): TimelineToolComponent | undefined {
	return TOOLS_BY_NAME[toolName];
}
