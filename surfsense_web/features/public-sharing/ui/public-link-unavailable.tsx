"use client";

import { Clock, Link2Off } from "lucide-react";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { PublicHeader } from "@/components/site/public-header";
import type { PublicLinkFailure } from "../lib/public-link-failure";

/** The page a public link shows when it has expired, was revoked, or could not load. */
export function PublicLinkUnavailable({ reason }: { reason: PublicLinkFailure }) {
	const t = useTranslations("publicSharing");
	const Icon = reason === "expired" ? Clock : Link2Off;
	const title =
		reason === "expired"
			? t("expired_title")
			: reason === "gone"
				? t("gone_title")
				: t("error_title");
	const body =
		reason === "expired" ? t("expired_body") : reason === "gone" ? t("gone_body") : t("error_body");

	return (
		<main className="min-h-screen overflow-x-hidden bg-main-panel text-foreground">
			<PublicHeader />
			<div className="flex h-screen flex-col items-center justify-center gap-6 px-4 text-center">
				<div className="flex size-16 items-center justify-center rounded-full bg-muted">
					<Icon className="size-8 text-muted-foreground" aria-hidden />
				</div>
				<div className="flex max-w-md flex-col gap-2">
					<h1 className="text-2xl font-semibold">{title}</h1>
					<p className="text-muted-foreground">{body}</p>
				</div>
				<Link href="/" className="text-sm text-primary underline-offset-4 hover:underline">
					{t("go_home")}
				</Link>
			</div>
		</main>
	);
}
