"use client";

import "@assistant-ui/react-markdown/styles/dot.css";

import { useMessagePartText } from "@assistant-ui/react";
import {
	MarkdownTextPrimitive,
	unstable_memoizeMarkdownComponents as memoizeMarkdownComponents,
	useIsMarkdownCodeBlock,
} from "@assistant-ui/react-markdown";
import { ExternalLinkIcon, FileIcon, Folder as FolderIcon } from "lucide-react";
import dynamic from "next/dynamic";
import { useTheme } from "next-themes";
import { createContext, memo, type ReactNode, useCallback, useContext, useRef } from "react";
import rehypeKatex from "rehype-katex";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import { ImagePreview, ImageRoot, ImageZoom } from "@/components/assistant-ui/image";
import { MentionChip } from "@/components/assistant-ui/mention-chip";
import "katex/dist/katex.min.css";
import { useCitationLabels } from "@/components/citations/citation-numbers-context";
import { processChildrenWithCitations } from "@/components/citations/citation-renderer";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { getVirtualPathDisplay } from "@/lib/chat/virtual-path-display";
import { type CitationUrlMap, preprocessCitationMarkdown } from "@/lib/citations/citation-parser";
import { normalizeAnswerMarkdown } from "@/lib/markdown/answer-markdown";
import { tryGetHostname } from "@/lib/url";
import { cn } from "@/lib/utils";

function MarkdownCodeBlockLoading() {
	return <div className="mt-4 h-32 overflow-hidden rounded-md bg-accent" />;
}

const LazyMarkdownCodeBlock = dynamic(
	() => import("./markdown-code-block").then((mod) => mod.MarkdownCodeBlock),
	{
		loading: () => <MarkdownCodeBlockLoading />,
	}
);

// Per-render URL placeholder map propagated to component overrides via
// React Context. Replaces the previous module-level `_pendingUrlCitations`
// state, which was unsafe under concurrent renders / SSR.
type CitationUrlMapRef = { current: CitationUrlMap };
const EMPTY_URL_MAP: CitationUrlMap = new Map();
const CitationUrlMapContext = createContext<CitationUrlMapRef>({ current: EMPTY_URL_MAP });

/**
 * Children with their citations drawn as chips. While the answer streams,
 * the bare "[3]" labels the model writes are chips too
 * (components/citations/citation-numbers-context.tsx), so a citation looks
 * and is numbered the same before and after the answer is saved.
 */
function useCitations(): (children: ReactNode) => ReactNode {
	const urlMap = useContext(CitationUrlMapContext).current;
	const labels = useCitationLabels();
	return (children) => processChildrenWithCitations(children, urlMap, labels);
}

/**
 * Preprocess raw markdown before it reaches the remark/rehype pipeline.
 * - Replaces URL-based citations with safe placeholders (prevents GFM autolinks)
 * - Normalises the rest (sandbox links, LaTeX delimiters, headings) as the
 *   copy does (lib/markdown/answer-markdown.ts)
 */
function preprocessMarkdown(content: string, urlMapRef: CitationUrlMapRef): string {
	// Replace URL-based citations with safe placeholders BEFORE markdown parsing.
	// GFM autolinks would otherwise convert the https://... inside [citation:URL]
	// into an <a> element, splitting the text and preventing our citation regex
	// from matching the full pattern.
	const { content: rewritten, urlMap } = preprocessCitationMarkdown(content);
	urlMapRef.current = urlMap;
	content = rewritten;

	return normalizeAnswerMarkdown(content);
}

const MarkdownTextImpl = () => {
	const urlMapRef = useRef<CitationUrlMap>(EMPTY_URL_MAP);
	const preprocess = useCallback((content: string) => preprocessMarkdown(content, urlMapRef), []);
	// The typing animation runs only while this part is streaming; a finished
	// part shows its text as it is. The animation advances on animation
	// frames, which a background tab never gets. When the turn ends there,
	// the saved answer replaces the streamed one with different text ([n]
	// becomes [citation:id]), and the animation would stop on what it had
	// shown so far: nothing, until the page was reloaded.
	const streaming = useMessagePartText().status.type === "running";
	return (
		<CitationUrlMapContext.Provider value={urlMapRef}>
			<MarkdownTextPrimitive
				smooth={streaming}
				remarkPlugins={[remarkGfm, [remarkMath, { singleDollarTextMath: false }]]}
				rehypePlugins={[rehypeKatex]}
				className="aui-md"
				components={defaultComponents}
				preprocess={preprocess}
			/>
		</CitationUrlMapContext.Provider>
	);
};

export const MarkdownText = memo(MarkdownTextImpl);

// A file path the chat writes is mount-prefixed: /<mount>/<relative/path>.
const LOCAL_FILE_PATH_REGEX = /^\/[a-z0-9_-]+\/[^\s`]+(?:\/[^\s`]+)*$/;

function isVirtualFilePathToken(value: string): boolean {
	if (!LOCAL_FILE_PATH_REGEX.test(value) || value.startsWith("//")) {
		return false;
	}
	const normalized = value.replace(/\/+$/, "");
	const segments = normalized.split("/").filter(Boolean);
	return segments.length >= 2;
}

function isStandaloneDocumentsPathText(node: ReactNode): string | null {
	if (typeof node !== "string") return null;
	const value = node.trim();
	if (!value.startsWith("/documents/")) return null;
	if (value.includes(" ")) return null;
	const normalized = value.replace(/\/+$/, "");
	const leaf = normalized.split("/").filter(Boolean).at(-1) ?? "";
	if (!leaf?.includes(".")) return null;
	return value;
}

// FilePathLink shows a /documents/... path the answer names as a chip. It
// opens nothing: the chat cites passages rather than giving documents file
// paths, so no route turns a path back into a document. The chip only keeps
// the path readable instead of a raw code span.
function FilePathLink({ path, className }: { path: string; className?: string }) {
	const { displayName, isFolder } = getVirtualPathDisplay(path);
	const icon = isFolder ? <FolderIcon className="size-3.5" /> : <FileIcon className="size-3.5" />;

	return (
		<MentionChip
			icon={icon}
			label={displayName || path}
			tooltip={path}
			ariaLabel={isFolder ? `Folder ${displayName}` : displayName}
			className={className}
		/>
	);
}

function MarkdownImage({ src, alt }: { src?: string; alt?: string }) {
	if (!src) return null;

	const domain = tryGetHostname(src) ?? "";

	return (
		<div className="my-4 w-fit max-w-lg overflow-hidden rounded-2xl border bg-muted/30 select-none">
			<ImageRoot variant="ghost" size="full">
				<ImageZoom src={src} alt={alt || "Image"}>
					<ImagePreview
						src={src}
						alt={alt || "Image"}
						className="max-h-[20rem] w-auto max-w-full object-contain"
					/>
				</ImageZoom>
			</ImageRoot>

			<div className="flex items-center justify-between px-5 py-3">
				<div className="min-w-0 flex-1">
					{alt && alt !== "Image" && (
						<p className="text-sm font-semibold text-foreground line-clamp-2">{alt}</p>
					)}
					{domain && <p className="text-xs text-muted-foreground mt-0.5 truncate">{domain}</p>}
				</div>
				<a
					href={src}
					target="_blank"
					rel="noopener noreferrer"
					className="ml-3 shrink-0 inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-medium text-foreground transition-colors hover:bg-muted"
					onClick={(e) => e.stopPropagation()}
				>
					Open
					<ExternalLinkIcon className="size-3" />
				</a>
			</div>
		</div>
	);
}

const defaultComponents = memoizeMarkdownComponents({
	h1: function H1({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<h1
				className={cn(
					"aui-md-h1 mb-8 scroll-m-20 font-extrabold text-4xl tracking-tight last:mb-0",
					className
				)}
				{...props}
			>
				{cite(children)}
			</h1>
		);
	},
	h2: function H2({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<h2
				className={cn(
					"aui-md-h2 mt-8 mb-4 scroll-m-20 font-semibold text-3xl tracking-tight first:mt-0 last:mb-0",
					className
				)}
				{...props}
			>
				{cite(children)}
			</h2>
		);
	},
	h3: function H3({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<h3
				className={cn(
					"aui-md-h3 mt-6 mb-4 scroll-m-20 font-semibold text-2xl tracking-tight first:mt-0 last:mb-0",
					className
				)}
				{...props}
			>
				{cite(children)}
			</h3>
		);
	},
	h4: function H4({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<h4
				className={cn(
					"aui-md-h4 mt-6 mb-4 scroll-m-20 font-semibold text-xl tracking-tight first:mt-0 last:mb-0",
					className
				)}
				{...props}
			>
				{cite(children)}
			</h4>
		);
	},
	h5: function H5({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<h5
				className={cn("aui-md-h5 my-4 font-semibold text-lg first:mt-0 last:mb-0", className)}
				{...props}
			>
				{cite(children)}
			</h5>
		);
	},
	h6: function H6({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<h6 className={cn("aui-md-h6 my-4 font-semibold first:mt-0 last:mb-0", className)} {...props}>
				{cite(children)}
			</h6>
		);
	},
	p: function P({ className, children, ...props }) {
		const cite = useCitations();
		const standalonePath = isStandaloneDocumentsPathText(children);
		return (
			<p className={cn("aui-md-p mt-5 mb-5 leading-7 first:mt-0 last:mb-0", className)} {...props}>
				{standalonePath ? <FilePathLink path={standalonePath} /> : cite(children)}
			</p>
		);
	},
	a: function A({ className, children, href, ...props }) {
		const cite = useCitations();
		// A model that has just written a file sometimes offers it as
		// [Download ...](sandbox:/workspace/thing.json). That path lives in the
		// throwaway container the deliverable was built in, which is destroyed
		// when the job ends, and no browser understands the scheme — so the
		// link resolved to nothing and the click just reloaded the page. The
		// artifact card above the message is the real download. Render the
		// words, not a link that cannot work.
		// Either the raw scheme, or the empty string react-markdown leaves
		// behind after stripping one it does not recognise.
		if (!href || /^sandbox:/i.test(href)) {
			return (
				<span
					className={cn("aui-md-a font-medium text-muted-foreground", className)}
					title="Use the Download button on the artifact above"
				>
					{cite(children)}
				</span>
			);
		}
		return (
			<a
				className={cn("aui-md-a font-medium text-primary underline underline-offset-4", className)}
				href={href}
				{...props}
			>
				{cite(children)}
			</a>
		);
	},
	blockquote: function Blockquote({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<blockquote className={cn("aui-md-blockquote border-l-2 pl-6 italic", className)} {...props}>
				{cite(children)}
			</blockquote>
		);
	},
	ul: ({ className, ...props }) => (
		<ul className={cn("aui-md-ul my-5 ml-6 list-disc [&>li]:mt-2", className)} {...props} />
	),
	ol: ({ className, ...props }) => (
		<ol className={cn("aui-md-ol my-5 ml-6 list-decimal [&>li]:mt-2", className)} {...props} />
	),
	li: function Li({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<li className={cn("aui-md-li", className)} {...props}>
				{cite(children)}
			</li>
		);
	},
	hr: ({ className, ...props }) => (
		<hr className={cn("aui-md-hr my-5 border-b", className)} {...props} />
	),
	table: ({ className, ...props }) => (
		<div className="aui-md-table-wrapper my-5 overflow-hidden rounded-md border">
			<Table className={cn("aui-md-table", className)} {...props} />
		</div>
	),
	thead: ({ className, ...props }) => (
		<TableHeader className={cn("aui-md-thead", className)} {...props} />
	),
	tbody: ({ className, ...props }) => (
		<TableBody className={cn("aui-md-tbody", className)} {...props} />
	),
	th: function Th({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<TableHead
				className={cn(
					"aui-md-th bg-muted/50 whitespace-normal [[align=center]]:text-center [[align=right]]:text-right",
					className
				)}
				{...props}
			>
				{cite(children)}
			</TableHead>
		);
	},
	td: function Td({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<TableCell
				className={cn(
					"aui-md-td whitespace-normal [[align=center]]:text-center [[align=right]]:text-right",
					className
				)}
				{...props}
			>
				{cite(children)}
			</TableCell>
		);
	},
	tr: ({ className, ...props }) => <TableRow className={cn("aui-md-tr", className)} {...props} />,
	sup: ({ className, ...props }) => (
		<sup className={cn("aui-md-sup [&>a]:text-xs [&>a]:no-underline", className)} {...props} />
	),
	pre: ({ children }) => <>{children}</>,
	code: function Code({ className, children, ...props }) {
		const isCodeBlock = useIsMarkdownCodeBlock();
		const { resolvedTheme } = useTheme();
		const language = /language-(\w+)/.exec(className || "")?.[1] ?? "text";
		const codeString = String(children).replace(/\n$/, "");
		// A fenced block holding nothing but one file path reads better as
		// inline code than as a highlighted block with a copy button.
		const isFilePathCodeBlock =
			isCodeBlock &&
			isVirtualFilePathToken(codeString.trim()) &&
			!codeString.trim().startsWith("//") &&
			!codeString.includes("\n");
		if (!isCodeBlock) {
			const inlineValue = String(children ?? "").trim();
			const normalizedInlinePath = inlineValue.replace(/\/+$/, "");
			const leafSegment = normalizedInlinePath.split("/").filter(Boolean).at(-1) ?? "";
			const isLikelyFolder =
				inlineValue.endsWith("/") || !leafSegment || !leafSegment.includes(".");
			const isLocalPath =
				isVirtualFilePathToken(inlineValue) &&
				!inlineValue.startsWith("//") &&
				!isLikelyFolder &&
				inlineValue.startsWith("/documents/");
			if (isLocalPath) {
				return <FilePathLink path={inlineValue} className="text-[0.9em]" />;
			}
			return (
				<code
					className={cn(
						"aui-md-inline-code rounded-md bg-primary/10 px-1.5 py-0.5 font-mono text-[0.9em] font-normal text-primary/80",
						className
					)}
					{...props}
				>
					{children}
				</code>
			);
		}
		if (isFilePathCodeBlock) {
			return (
				<code
					className={cn(
						"aui-md-inline-code rounded-md bg-primary/10 px-1.5 py-0.5 font-mono text-[0.9em] font-normal text-primary/80",
						className
					)}
					{...props}
				>
					{codeString.trim()}
				</code>
			);
		}
		return (
			<LazyMarkdownCodeBlock
				className={className}
				language={language}
				codeText={codeString}
				isDarkMode={resolvedTheme === "dark"}
			/>
		);
	},
	strong: function Strong({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<strong className={cn("aui-md-strong font-semibold", className)} {...props}>
				{cite(children)}
			</strong>
		);
	},
	em: function Em({ className, children, ...props }) {
		const cite = useCitations();
		return (
			<em className={cn("aui-md-em", className)} {...props}>
				{cite(children)}
			</em>
		);
	},
	img: ({ src, alt }) => (
		<MarkdownImage src={typeof src === "string" ? src : undefined} alt={alt} />
	),
	CodeHeader: () => null,
});
