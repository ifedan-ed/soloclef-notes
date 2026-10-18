import {
	AlarmClock,
	Brain,
	FileCheck2,
	FileEdit,
	FilePlus,
	FileText,
	FileX,
	Film,
	FolderPlus,
	FolderTree,
	FolderX,
	ImageIcon,
	ListTodo,
	type LucideIcon,
	Move,
	Podcast,
	ScanLine,
	SearchCheck,
	Wrench,
} from "lucide-react";

/**
 * Every tool now renders a card via ``ToolFallback``. The icon map is
 * keyed on the canonical backend tool name (registered in
 * ``surfsense_backend/app/agents/new_chat/tools/registry.py``); unknown
 * names fall back to the generic ``Wrench`` icon so the card still
 * communicates "this is a tool call".
 */
const TOOL_ICONS: Record<string, LucideIcon> = {
	// Generators
	generate_podcast: Podcast,
	generate_video_presentation: Film,
	generate_image: ImageIcon,
	display_image: ImageIcon,
	// Web / search
	scrape_webpage: ScanLine,
	// Automations
	create_automation: AlarmClock,
	// Memory
	update_memory: Brain,
	// Filesystem (built-in deepagent + middleware)
	read_file: FileText,
	write_file: FilePlus,
	edit_file: FileEdit,
	move_file: Move,
	rm: FileX,
	rmdir: FolderX,
	mkdir: FolderPlus,
	ls: FolderTree,
	write_todos: ListTodo,
	// Google Drive
	create_google_drive_file: FilePlus,
	delete_google_drive_file: FileX,
	// Misc
	get_connected_accounts: SearchCheck,
	execute: Wrench,
	execute_code: Wrench,
	verify_artifact: FileCheck2,
};

export function getToolIcon(name: string): LucideIcon {
	return TOOL_ICONS[name] ?? Wrench;
}

/**
 * Friendly display names for tools shown in the chat UI.
 *
 * Most users aren't engineers; they shouldn't see raw unix-style
 * identifiers like ``rm`` / ``rmdir`` / ``ls`` / ``grep`` / ``glob`` or
 * snake_cased function names. The map below renders each tool with
 * plain English wording (verb + object) so non-technical users
 * understand what the agent is doing at a glance.
 *
 * Unmapped tool names fall back to a snake_case-to-Title-Case
 * conversion via :func:`getToolDisplayName`.
 */
const TOOL_DISPLAY_NAMES: Record<string, string> = {
	// Filesystem / knowledge base
	read_file: "Read file",
	write_file: "Write file",
	edit_file: "Edit file",
	move_file: "Move file",
	rm: "Delete file",
	rmdir: "Delete folder",
	mkdir: "Create folder",
	ls: "List files",
	glob: "Find files",
	grep: "Search in files",
	write_todos: "Plan tasks",
	save_document: "Save document",
	// Generators
	generate_podcast: "Generate podcast",
	generate_video_presentation: "Generate video presentation",
	generate_image: "Generate image",
	display_image: "Show image",
	// Web / search
	scrape_webpage: "Read webpage",
	// Automations
	create_automation: "Create automation",
	// Memory
	update_memory: "Update memory",
	// Google Drive
	create_google_drive_file: "Create Google Drive file",
	delete_google_drive_file: "Delete Google Drive file",
	// Misc
	get_connected_accounts: "Discover connected accounts",
	execute: "Run command",
	execute_code: "Run code",
	verify_artifact: "Verify document",
};

/**
 * Format a tool's canonical (snake_case) name for display in the chat UI.
 *
 * Looks up :data:`TOOL_DISPLAY_NAMES` first; falls back to a
 * snake_case-to-Title-Case rewrite for tools that don't have a curated
 * label (e.g. dynamically registered MCP tools).
 */
export function getToolDisplayName(name: string): string {
	const friendly = TOOL_DISPLAY_NAMES[name];
	if (friendly) return friendly;
	return name.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

export const CONNECTOR_TOOL_ICON_PATHS: Record<string, { src: string; alt: string }> = {
	google_drive: { src: "/connectors/google-drive.svg", alt: "Google Drive" },
};

export const CONNECTOR_ICON_TO_TYPES: Record<string, string[]> = {
	google_drive: ["GOOGLE_DRIVE_CONNECTOR"],
};
