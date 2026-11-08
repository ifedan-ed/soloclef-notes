"use client";

import { MarkdownPlugin, remarkMdx } from "@platejs/markdown";
import { slateToHtml } from "@slate-serializers/html";
import type { AnyPluginConfig, Descendant, Value } from "platejs";
import { createPlatePlugin, Key, Plate, useEditorReadOnly, usePlateEditor } from "platejs/react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import remarkGfm from "remark-gfm";
import { EditorSaveContext } from "@/components/editor/editor-save-context";
import { CitationKit, citationMarkdownRules } from "@/components/editor/plugins/citation-kit";
import { MathKit } from "@/components/editor/plugins/math-kit";
import { type EditorPreset, presetMap } from "@/components/editor/presets";
import { editedMarkdownReporter } from "@/components/editor/utils/edited-markdown";
import { mathReading, readMarkdown, writeMarkdown } from "@/components/editor/utils/read-markdown";
import { Editor, EditorContainer } from "@/components/ui/editor";

/** Live editor instance returned by `usePlateEditor`. */
export type PlateEditorInstance = ReturnType<typeof usePlateEditor>;

export interface PlateEditorProps {
	/** Markdown string to load as initial content */
	markdown?: string;
	/** HTML string to load as initial content. Takes precedence over `markdown`. */
	html?: string;
	/**
	 * Called when the person edits the text, with serialized markdown. Not
	 * called for a click, a selection or the editor's own tidying; an edit
	 * undone gives `markdown` back as it was passed (utils/edited-markdown.ts).
	 */
	onMarkdownChange?: (markdown: string) => void;
	/** Called when the editor content changes, with serialized HTML. Use with the `html` prop. */
	onHtmlChange?: (html: string) => void;
	/**
	 * Force permanent read-only mode (e.g. public/shared view).
	 * When true, the editor cannot be toggled to editing mode.
	 * When false (default), the editor starts in viewing mode but
	 * the user can switch to editing via the mode toolbar button.
	 */
	readOnly?: boolean;
	/** Placeholder text */
	placeholder?: string;
	/** Editor container variant */
	variant?: "default" | "demo" | "comment" | "select";
	/** Editor text variant */
	editorVariant?: "default" | "demo" | "fullWidth" | "none";
	/** Additional className for the container */
	className?: string;
	/** Save callback. When provided, ⌘+Shift+S / Ctrl+Shift+S shortcut is registered (avoiding the browser's ⌘+S / Ctrl+S "Save Page As" conflict) and a save button appears in the toolbar. */
	onSave?: () => void;
	/** Whether there are unsaved changes */
	hasUnsavedChanges?: boolean;
	/** Whether a save is in progress */
	isSaving?: boolean;
	/** Whether edit/view mode toggle UI should be available in toolbars. */
	allowModeToggle?: boolean;
	/** Reserve fixed-toolbar vertical space even when controls are hidden. */
	reserveToolbarSpace?: boolean;
	/** Start the editor in editing mode instead of viewing mode. Ignored when readOnly is true. */
	defaultEditing?: boolean;
	/**
	 * Plugin preset to use. Controls which plugin kits are loaded.
	 * - "full"     – all plugins (toolbars, slash commands, DnD, etc.)
	 * - "minimal"  – core formatting only (no fixed toolbar, slash commands, DnD, block selection)
	 * - "note"     – minimal plus tables and maths, for notes kept from chat answers
	 * - "readonly" – rendering support for all rich content, no editing UI
	 * @default "full"
	 */
	preset?: EditorPreset;
	/**
	 * Additional plugins to append after the preset plugins.
	 * Use this to inject feature-specific plugins (e.g. approve/reject blocks)
	 * without modifying the core editor component.
	 */
	extraPlugins?: AnyPluginConfig[];
	/**
	 * Render `[citation:N]` and `[citation:URL]` tokens in the deserialized
	 * markdown as interactive citation badges/popovers (mirrors chat). Only
	 * meant for read-only views — when true, `onMarkdownChange` is suppressed
	 * because the in-memory tree contains custom inline-void elements that
	 * have no markdown serialize rule.
	 */
	enableCitations?: boolean;
	/**
	 * Read and write maths as chat answers do (`\(…\)`, `\[…\]` and `$$…$$`
	 * drawn as formulas, a single `$` left as text; utils/chat-math.ts), with
	 * the equation plugins loaded whatever the preset. For notes, which are
	 * often answers kept from a chat.
	 */
	chatMath?: boolean;
	/**
	 * Draw `[citation:…]` markers as the chat's citation chips in an editor
	 * that can be edited: a chip can be deleted, not changed, and is written
	 * back as its marker (utils/citation-nodes.ts). For notes, which are often
	 * answers kept from a chat. `enableCitations` is the read-only form.
	 */
	chatCitations?: boolean;
}

function PlateEditorContent({
	editorVariant,
	placeholder,
}: {
	editorVariant: PlateEditorProps["editorVariant"];
	placeholder?: string;
}) {
	const isReadOnly = useEditorReadOnly();

	return (
		<Editor
			variant={editorVariant}
			placeholder={isReadOnly ? undefined : placeholder}
			className="min-h-full"
		/>
	);
}

export function PlateEditor({
	markdown,
	html,
	onMarkdownChange,
	onHtmlChange,
	readOnly = false,
	placeholder = "Type...",
	variant = "default",
	editorVariant = "default",
	className,
	onSave,
	hasUnsavedChanges = false,
	isSaving = false,
	allowModeToggle = true,
	reserveToolbarSpace = false,
	defaultEditing = false,
	preset = "full",
	extraPlugins = [],
	enableCitations = false,
	chatMath = false,
	chatCitations = false,
}: PlateEditorProps) {
	const lastMarkdownRef = useRef(markdown);
	const lastHtmlRef = useRef(html);

	// Keep a stable ref to the latest onSave callback so the plugin shortcut
	// always calls the most recent version without re-creating the editor.
	const onSaveRef = useRef(onSave);
	useEffect(() => {
		onSaveRef.current = onSave;
	}, [onSave]);

	const SaveShortcutPlugin = useMemo(
		() =>
			createPlatePlugin({
				key: "save-shortcut",
				shortcuts: {
					save: {
						keys: [[Key.Mod, Key.Shift, "s"]],
						handler: () => {
							onSaveRef.current?.();
						},
						preventDefault: true,
					},
				},
			}),
		[]
	);

	// Resolve the plugin set from the chosen preset
	const presetPlugins = presetMap[preset];
	const mathPlugins = chatMath && !presetPlugins.includes(MathKit[0]) ? MathKit : [];
	const citationChips = enableCitations || chatCitations;
	const reading = { chatMath, citations: citationChips };

	// When readOnly is forced, always start in readOnly.
	// Otherwise, respect defaultEditing to decide initial mode.
	// The user can still toggle between editing/viewing via ModeToolbarButton.
	const editor = usePlateEditor({
		readOnly: readOnly || !defaultEditing,
		plugins: [
			...presetPlugins,
			...mathPlugins,
			// Only register save shortcut when a save handler is provided
			...(onSave ? [SaveShortcutPlugin] : []),
			// Consumer-provided extra plugins
			...extraPlugins,
			// Citation void inline element (read-only document viewer, notes).
			...(citationChips ? CitationKit : []),
			MarkdownPlugin.configure({
				options: {
					remarkPlugins: [remarkGfm, mathReading(chatMath), remarkMdx],
					...(citationChips ? { rules: citationMarkdownRules } : {}),
				},
			}),
		],
		value: html
			? (editor) => editor.api.html.deserialize({ element: html }) as Value
			: markdown
				? (editor) => readMarkdown(editor, markdown, reading) as Value
				: undefined,
	});

	// What a change reports (utils/edited-markdown.ts), measured against the
	// text as loaded, before any change; made again when the markdown prop
	// loads another text.
	const reporterFor = useCallback(
		(loaded: string, value: Value) => editedMarkdownReporter(loaded, writeMarkdown(editor, value)),
		[editor]
	);
	const [firstReporter] = useState(() =>
		onMarkdownChange && !enableCitations ? reporterFor(markdown ?? "", editor.children) : null
	);
	const reportRef = useRef(firstReporter);

	// Update editor content when html prop changes externally
	useEffect(() => {
		if (html !== undefined && html !== lastHtmlRef.current) {
			lastHtmlRef.current = html;
			const newValue = editor.api.html.deserialize({ element: html });
			editor.tf.reset();
			editor.tf.setValue(newValue);
		}
	}, [html, editor]);

	// Update editor content when markdown prop changes externally
	// (e.g., version switching in report panel)
	useEffect(() => {
		if (!html && markdown !== undefined && markdown !== lastMarkdownRef.current) {
			lastMarkdownRef.current = markdown;
			const newValue = readMarkdown(editor, markdown, { chatMath, citations: citationChips });
			editor.tf.reset();
			editor.tf.setValue(newValue as Value);
			reportRef.current = reporterFor(markdown, editor.children);
		}
	}, [html, markdown, editor, citationChips, chatMath, reporterFor]);

	// When not forced read-only, the user can toggle between editing/viewing.
	const canToggleMode = !readOnly && allowModeToggle;

	const contextProviderValue = useMemo(
		() => ({
			onSave,
			hasUnsavedChanges,
			isSaving,
			canToggleMode,
			reserveToolbarSpace,
		}),
		[onSave, hasUnsavedChanges, isSaving, canToggleMode, reserveToolbarSpace]
	);

	return (
		<EditorSaveContext.Provider value={contextProviderValue}>
			<Plate
				editor={editor}
				// Only pass readOnly as a controlled prop when forced (permanently read-only).
				// For non-forced mode, the Plate store manages readOnly internally
				// (initialized to true via usePlateEditor, toggled via ModeToolbarButton).
				{...(readOnly ? { readOnly: true } : {})}
				onChange={({ value }) => {
					// View-only citation mode: skip serialization. The custom
					// `citation` inline-void element has no markdown serialize
					// rule, so emitting changes here would overwrite
					// `lastMarkdownRef.current` (and downstream copy-to-clipboard
					// state in EditorPanelContent) with a tree that loses every
					// citation token. `enableCitations` is only ever set in
					// read-only paths, so user input cannot reach this branch
					// in practice — the guard exists for the initial Plate
					// normalize emit.
					if (enableCitations) return;
					if (onHtmlChange && html) {
						const serialized = slateToHtml(value as Descendant[]);
						onHtmlChange(serialized);
					} else if (onMarkdownChange) {
						reportRef.current ??= reporterFor(lastMarkdownRef.current ?? "", value);
						const md = reportRef.current(writeMarkdown(editor, value));
						if (md === null) return;
						lastMarkdownRef.current = md;
						onMarkdownChange(md);
					}
				}}
			>
				<EditorContainer variant={variant} className={className}>
					<PlateEditorContent editorVariant={editorVariant} placeholder={placeholder} />
				</EditorContainer>
			</Plate>
		</EditorSaveContext.Provider>
	);
}
