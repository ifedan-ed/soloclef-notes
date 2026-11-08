"use client";

import { CheckIcon, CopyIcon } from "lucide-react";
import { memo, useEffect, useState } from "react";

import { MermaidDiagram } from "@/components/assistant-ui/mermaid-diagram";
import { Button } from "@/components/ui/button";
import { HighlightedCode } from "@/features/code-highlighting/highlighted-code";
import { copyToClipboard } from "@/lib/utils";

type MarkdownCodeBlockProps = {
	className?: string;
	language: string;
	codeText: string;
	isDarkMode: boolean;
};

function MarkdownCodeBlockComponent({
	className,
	language,
	codeText,
	isDarkMode,
}: MarkdownCodeBlockProps) {
	const [hasCopied, setHasCopied] = useState(false);
	const normalizedLanguage = language.toLowerCase();

	useEffect(() => {
		if (!hasCopied) return;
		const timer = setTimeout(() => setHasCopied(false), 2000);
		return () => clearTimeout(timer);
	}, [hasCopied]);

	const codeBlock = (
		<div className="mt-4 overflow-hidden rounded-md bg-accent">
			<div className="flex items-center justify-between gap-4 px-4 py-2 font-semibold text-muted-foreground text-sm">
				<span className="lowercase text-xs">{language}</span>
				<Button
					variant="ghost"
					size="sm"
					className="h-8 w-8 p-0"
					type="button"
					onClick={async () => {
						const ok = await copyToClipboard(codeText);
						if (ok) setHasCopied(true);
					}}
					aria-label={hasCopied ? "Copied code" : "Copy code"}
				>
					<span className="sr-only">Copy</span>
					{hasCopied ? <CheckIcon className="!size-3" /> : <CopyIcon className="!size-3" />}
				</Button>
			</div>

			<pre className="overflow-x-auto bg-syntax-bg px-4 py-3 font-mono text-sm leading-relaxed">
				<HighlightedCode language={normalizedLanguage} source={codeText} className={className} />
			</pre>
		</div>
	);

	if (normalizedLanguage === "mermaid") {
		return <MermaidDiagram source={codeText} isDarkMode={isDarkMode} fallback={codeBlock} />;
	}

	return codeBlock;
}

export const MarkdownCodeBlock = memo(MarkdownCodeBlockComponent);
