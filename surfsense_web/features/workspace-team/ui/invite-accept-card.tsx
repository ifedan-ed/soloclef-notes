"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, CheckCircle2, LogIn, LogOut, Mail, XCircle } from "lucide-react";
import Image from "next/image";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardFooter,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import type { AcceptInviteResponse } from "@/contracts/types/invites.types";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { useSession } from "@/hooks/use-session";
import { invitesApiService } from "@/lib/apis/invites-api.service";
import { logout, setRedirectPath } from "@/lib/auth-utils";
import { BRAND } from "@/lib/brand";
import { AppError } from "@/lib/error";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { workspaceHomePath } from "@/lib/workspace-home";
import { apiErrorDetail } from "../lib/api-error-detail";

const PENDING_KEY = "pending_invite_code";

/** The page an invitation link opens: who invited you where, and accept it as that account. */
export function InviteAcceptCard({ inviteCode }: { inviteCode: string }) {
	const t = useTranslations("invitePage");
	const router = useRouter();
	const queryClient = useQueryClient();
	const session = useSession();

	const { data: info, isLoading } = useQuery({
		queryKey: cacheKeys.invites.info(inviteCode),
		enabled: !!inviteCode,
		staleTime: 60 * 1000,
		queryFn: () => invitesApiService.getInviteInfo({ invite_code: inviteCode }),
	});

	const [accepting, setAccepting] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [wrongAccount, setWrongAccount] = useState(false);
	const [joined, setJoined] = useState<AcceptInviteResponse | null>(null);
	const showAccepting = useDelayedFlag(accepting);
	const autoTried = useRef(false);

	const accept = async () => {
		if (accepting) return;
		setAccepting(true);
		setError(null);
		setWrongAccount(false);
		try {
			const result = await invitesApiService.acceptInvite({ invite_code: inviteCode });
			await queryClient.invalidateQueries({ queryKey: cacheKeys.workspaces.all });
			setJoined(result);
		} catch (err) {
			setWrongAccount(err instanceof AppError && err.status === 403);
			setError(apiErrorDetail(err, t("accept_failed")));
		} finally {
			setAccepting(false);
		}
	};

	// Back from signing in for this invite: accept it once, as whoever signed in.
	// biome-ignore lint/correctness/useExhaustiveDependencies: runs once per sign-in
	useEffect(() => {
		if (session.status !== "authenticated" || !info?.is_valid || autoTried.current) return;
		let pending: string | null = null;
		try {
			pending = localStorage.getItem(PENDING_KEY);
			if (pending === inviteCode) localStorage.removeItem(PENDING_KEY);
		} catch {
			pending = null;
		}
		if (pending === inviteCode) {
			autoTried.current = true;
			void accept();
		}
	}, [session.status, info?.is_valid, inviteCode]);

	const signIn = () => {
		try {
			localStorage.setItem(PENDING_KEY, inviteCode);
		} catch {
			// Private mode: they accept by hand after signing in.
		}
		setRedirectPath(`/invite/${inviteCode}`);
		router.push("/login");
	};

	const switchAccount = async () => {
		await logout();
		setRedirectPath(`/invite/${inviteCode}`);
		try {
			localStorage.setItem(PENDING_KEY, inviteCode);
		} catch {
			// ignored
		}
		window.location.assign("/login");
	};

	const workspace = info?.workspace_name || t("workspace_fallback");
	const role = info?.role_name ?? "";
	const loading = isLoading || session.status === "loading";

	return (
		<div className="flex min-h-screen items-center justify-center bg-background p-4">
			<div className="w-full max-w-md">
				<Card className="shadow-lg">
					{loading ? (
						<CardContent className="space-y-4 py-10" aria-busy="true" aria-label={t("loading")}>
							<Skeleton className="mx-auto size-14 rounded-full" />
							<Skeleton className="mx-auto h-6 w-48" />
							<Skeleton className="mx-auto h-4 w-64" />
						</CardContent>
					) : joined ? (
						<>
							<CardHeader className="text-center">
								<CheckCircle2 className="mx-auto mb-2 size-12 text-emerald-500" />
								<CardTitle className="text-2xl">{t("joined_title")}</CardTitle>
								<CardDescription>
									{t("joined_body", {
										workspace: joined.workspace_name,
										role: joined.role_name ?? "",
									})}
								</CardDescription>
							</CardHeader>
							<CardFooter>
								<Button
									className="w-full"
									onClick={() => router.push(workspaceHomePath(joined.workspace_id))}
								>
									{t("open_workspace")}
								</Button>
							</CardFooter>
						</>
					) : !info?.is_valid ? (
						<>
							<CardHeader className="text-center">
								<XCircle className="mx-auto mb-2 size-12 text-destructive" />
								<CardTitle className="text-2xl">{t("invalid_title")}</CardTitle>
								<CardDescription>{info?.message || t("invalid_fallback")}</CardDescription>
							</CardHeader>
							<CardContent>
								<p className="text-center text-sm text-muted-foreground">{t("invalid_hint")}</p>
							</CardContent>
							<CardFooter>
								<Button
									variant="outline"
									className="w-full"
									onClick={() => router.push("/dashboard")}
								>
									{t("go_home")}
								</Button>
							</CardFooter>
						</>
					) : (
						<>
							<CardHeader className="text-center">
								<Mail className="mx-auto mb-2 size-12 text-primary" />
								<CardTitle className="text-2xl">{t("invited_title")}</CardTitle>
								<CardDescription>
									{info.inviter_name
										? t("invited_by", { inviter: info.inviter_name, workspace, role })
										: t("invited_generic", { workspace, role })}
								</CardDescription>
							</CardHeader>
							<CardContent className="space-y-3">
								{info.invited_email_hint && (
									<p className="rounded-lg bg-muted/60 p-3 text-center text-sm">
										{t("for_address", { address: info.invited_email_hint })}
									</p>
								)}
								{error && (
									<div
										role="alert"
										className="space-y-2 rounded-lg bg-destructive/10 p-3 text-sm text-destructive"
									>
										<p className="flex items-start gap-2">
											<AlertCircle className="mt-0.5 size-4 shrink-0" />
											<span>{error}</span>
										</p>
										{wrongAccount && (
											<Button
												variant="outline"
												size="sm"
												className="w-full gap-1.5"
												onClick={() => void switchAccount()}
											>
												<LogOut className="size-4" />
												{t("switch_account")}
											</Button>
										)}
									</div>
								)}
							</CardContent>
							<CardFooter className="flex gap-2">
								{session.authenticated ? (
									<>
										<Button
											variant="outline"
											className="flex-1"
											onClick={() => router.push("/dashboard")}
										>
											{t("decline")}
										</Button>
										<Button
											className="flex-1 gap-1.5"
											onClick={() => void accept()}
											disabled={accepting}
										>
											{showAccepting ? <Spinner size="sm" /> : <CheckCircle2 className="size-4" />}
											{accepting ? t("accepting") : t("accept")}
										</Button>
									</>
								) : (
									<Button className="w-full gap-1.5" onClick={signIn}>
										<LogIn className="size-4" />
										{t("sign_in")}
									</Button>
								)}
							</CardFooter>
						</>
					)}
				</Card>
				<div className="mt-6 text-center">
					<Link
						href="/"
						className="inline-flex items-center gap-2 text-muted-foreground transition-colors hover:text-foreground"
					>
						<Image
							src="/icon-128.svg"
							alt={BRAND.name}
							width={24}
							height={24}
							className="rounded"
						/>
						<span className="text-sm font-medium">{BRAND.name}</span>
					</Link>
				</div>
			</div>
		</div>
	);
}
