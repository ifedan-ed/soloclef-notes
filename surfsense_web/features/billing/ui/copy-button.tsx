"use client";

import { Check, Copy } from "lucide-react";
import { useTranslations } from "next-intl";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";

/** Copies a value to the clipboard and confirms inline for a moment. */
export function CopyButton({ value, label }: { value: string; label: string }) {
	const t = useTranslations("billing");
	const [copied, setCopied] = useState(false);
	useEffect(() => {
		if (!copied) return;
		const timer = window.setTimeout(() => setCopied(false), 1500);
		return () => window.clearTimeout(timer);
	}, [copied]);
	return (
		<Button
			type="button"
			variant="outline"
			size="icon"
			className="size-9 shrink-0"
			aria-label={copied ? t("copied") : label}
			onClick={async () => {
				try {
					await navigator.clipboard.writeText(value);
					setCopied(true);
				} catch {
					toast.error(t("copy_failed"));
				}
			}}
		>
			{copied ? <Check className="size-4" aria-hidden /> : <Copy className="size-4" aria-hidden />}
		</Button>
	);
}
