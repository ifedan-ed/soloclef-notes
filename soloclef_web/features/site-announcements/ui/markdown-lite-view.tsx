import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { type Inline, parseMarkdownLite } from "../lib/markdown-lite";

function renderInline(inline: Inline, key: number): ReactNode {
	switch (inline.kind) {
		case "text":
			return inline.text;
		case "strong":
			return <strong key={key}>{inline.text}</strong>;
		case "em":
			return <em key={key}>{inline.text}</em>;
		case "code":
			return (
				<code key={key} className="rounded bg-muted px-1 font-mono text-[0.9em]">
					{inline.text}
				</code>
			);
		case "link": {
			const external = !/^\/(?![\/\\])/.test(inline.href);
			return (
				<a
					key={key}
					href={inline.href}
					className="font-medium text-primary underline underline-offset-2"
					onClick={(event) => event.stopPropagation()}
					{...(external ? { target: "_blank", rel: "noopener noreferrer" } : {})}
				>
					{inline.text}
				</a>
			);
		}
		case "break":
			return <br key={key} />;
	}
}

/** An announcement body in its small Markdown: paragraphs, bullets, bold, italic, code, links. */
export function MarkdownLiteView({ source, className }: { source: string; className?: string }) {
	const blocks = parseMarkdownLite(source);
	return (
		<div className={cn("space-y-1.5 break-words", className)}>
			{blocks.map((block, i) =>
				block.kind === "list" ? (
					// biome-ignore lint/suspicious/noArrayIndexKey: blocks have no identity beyond order
					<ul key={i} className="list-disc space-y-0.5 pl-4">
						{block.items.map((item, j) => (
							// biome-ignore lint/suspicious/noArrayIndexKey: items have no identity beyond order
							<li key={j}>{item.map(renderInline)}</li>
						))}
					</ul>
				) : (
					// biome-ignore lint/suspicious/noArrayIndexKey: blocks have no identity beyond order
					<p key={i}>{block.inlines.map(renderInline)}</p>
				)
			)}
		</div>
	);
}
