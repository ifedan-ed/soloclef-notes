import {
	AlarmClock,
	BookOpen,
	BookPlus,
	Brain,
	ChartColumn,
	FileCheck2,
	FileEdit,
	FilePlus,
	FileText,
	FileX,
	FolderOpen,
	FolderPlus,
	FolderSearch,
	FolderTree,
	FolderX,
	Globe,
	ImageIcon,
	ListTodo,
	type LucideIcon,
	Mail,
	Move,
	Podcast,
	ScanLine,
	Search,
	SearchCheck,
	Wrench,
} from "lucide-react";

/**
 * Every tool now renders a card via ``ToolFallback``. The icon map is
 * keyed on the canonical tool name the chat's agent registers (the API's
 * chat and soloclef_assistant); unknown
 * names fall back to the generic ``Wrench`` icon so the card still
 * communicates "this is a tool call".
 */
const TOOL_ICONS: Record<string, LucideIcon> = {
	// Generators
	generate_podcast: Podcast,
	draw_figure: ChartColumn,
	// Only in chats saved before figures replaced images.
	generate_image: ImageIcon,
	display_image: ImageIcon,
	// Web / search
	scrape_webpage: ScanLine,
	web_crawl: ScanLine,
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
	// The knowledge-base specialist's tools (soloclef_api/internal/knowledgebase)
	knowledge_base: FolderOpen,
	list_folder: FolderTree,
	find_documents: FolderSearch,
	read_document: FileText,
	create_note: FilePlus,
	edit_note: FileEdit,
	rename_document: FileEdit,
	move_document: Move,
	delete_note: FileX,
	create_folder: FolderPlus,
	rename_folder: FolderOpen,
	move_folder: Move,
	delete_folder: FolderX,
	write_todos: ListTodo,
	// The chat's own searches
	search_knowledge_base: BookOpen,
	add_sources: BookPlus,
	exa_web_search: Globe,
	tavily_web_search: Globe,
	pubmed_search: Search,
	arxiv_search: Search,
	preprint_search: Search,
	// Mail
	list_email_accounts: Mail,
	list_email_folders: FolderTree,
	search_emails: Mail,
	read_email: Mail,
	read_email_thread: Mail,
	draft_email: FileEdit,
	// Google Drive and Nextcloud
	search_google_drive: FolderSearch,
	read_google_drive_file: FileText,
	create_google_drive_file: FilePlus,
	trash_google_drive_file: FileX,
	delete_google_drive_file: FileX,
	search_nextcloud_files: FolderSearch,
	read_nextcloud_file: FileText,
	create_nextcloud_file: FilePlus,
	trash_nextcloud_file: FileX,
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
	knowledge_base: "Work in your documents",
	list_folder: "List folder",
	find_documents: "Find documents",
	read_document: "Read document",
	create_note: "Write note",
	edit_note: "Edit note",
	rename_document: "Rename document",
	move_document: "Move document",
	delete_note: "Delete note",
	create_folder: "Create folder",
	rename_folder: "Rename folder",
	move_folder: "Move folder",
	delete_folder: "Delete folder",
	// Generators
	generate_podcast: "Generate podcast",
	draw_figure: "Draw figure",
	// Only in chats saved before figures replaced images.
	generate_image: "Generate image",
	display_image: "Show image",
	// Web / search
	scrape_webpage: "Read webpage",
	web_crawl: "Read web pages",
	// Automations
	create_automation: "Create automation",
	// Memory
	update_memory: "Update memory",
	// The chat's own searches (soloclef_assistant: documents, websearch, academic)
	search_knowledge_base: "Search your sources",
	add_sources: "Add sources",
	exa_web_search: "Search the web with Exa",
	tavily_web_search: "Search the web with Tavily",
	pubmed_search: "Search PubMed",
	arxiv_search: "Search arXiv",
	preprint_search: "Search bioRxiv and medRxiv",
	// Mail (soloclef_assistant/mailtools)
	list_email_accounts: "List your mailboxes",
	list_email_folders: "List mail folders",
	search_emails: "Search your email",
	read_email: "Read an email",
	read_email_thread: "Read a conversation",
	draft_email: "Draft an email",
	// Google Drive (soloclef_assistant/drivetools)
	search_google_drive: "Search Google Drive",
	read_google_drive_file: "Read a Drive file",
	create_google_drive_file: "Create a Drive file",
	trash_google_drive_file: "Move a Drive file to the bin",
	delete_google_drive_file: "Delete Google Drive file",
	// Nextcloud (soloclef_assistant/nextcloudtools)
	search_nextcloud_files: "Search Nextcloud",
	read_nextcloud_file: "Read a Nextcloud file",
	create_nextcloud_file: "Create a Nextcloud file",
	trash_nextcloud_file: "Move a Nextcloud file to the bin",
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
