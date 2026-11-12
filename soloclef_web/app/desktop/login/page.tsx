"use client";

import { Crop, Eye, EyeOff, Rocket, RotateCcw, Zap } from "lucide-react";
import Image from "next/image";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { DEFAULT_SHORTCUTS, keyEventToAccelerator } from "@/components/desktop/shortcut-recorder";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { ShortcutKbd } from "@/components/ui/shortcut-kbd";
import { Spinner } from "@/components/ui/spinner";
import { useElectronAPI } from "@/hooks/use-platform";
import { getPostLoginRedirectPath } from "@/lib/auth-utils";
import { BRAND } from "@/lib/brand";

type ShortcutKey = "generalAssist" | "quickAsk" | "screenshotAssist";
type ShortcutMap = typeof DEFAULT_SHORTCUTS;

const HOTKEY_ROWS: Array<{
	key: ShortcutKey;
	label: string;
	description: string;
	icon: React.ElementType;
}> = [
	{
		key: "generalAssist",
		label: "General Assist",
		description: `Launch ${BRAND.name} instantly from any application`,
		icon: Rocket,
	},
	{
		key: "screenshotAssist",
		label: "Screenshot Assist",
		description: "Draw a region on screen to attach that capture to chat",
		icon: Crop,
	},
	{
		key: "quickAsk",
		label: "Quick Assist",
		description: "Select text anywhere, then ask AI to explain, rewrite, or act on it",
		icon: Zap,
	},
];

function acceleratorToKeys(accel: string, isMac: boolean): string[] {
	if (!accel) return [];
	return accel.split("+").map((part) => {
		if (part === "CommandOrControl") {
			return isMac ? "⌘" : "Ctrl";
		}
		if (part === "Alt") {
			return isMac ? "⌥" : "Alt";
		}
		if (part === "Shift") {
			return isMac ? "⇧" : "Shift";
		}
		if (part === "Space") return "Space";
		return part.length === 1 ? part.toUpperCase() : part;
	});
}

function HotkeyRow({
	label,
	description,
	value,
	defaultValue,
	icon: Icon,
	isMac,
	onChange,
	onReset,
}: {
	label: string;
	description: string;
	value: string;
	defaultValue: string;
	icon: React.ElementType;
	isMac: boolean;
	onChange: (accelerator: string) => void;
	onReset: () => void;
}) {
	const [recording, setRecording] = useState(false);
	const inputRef = useRef<HTMLButtonElement>(null);
	const isDefault = value === defaultValue;
	const displayKeys = useMemo(() => acceleratorToKeys(value, isMac), [value, isMac]);

	const handleKeyDown = useCallback(
		(e: React.KeyboardEvent) => {
			if (!recording) return;
			e.preventDefault();
			e.stopPropagation();

			if (e.key === "Escape") {
				setRecording(false);
				return;
			}

			const accel = keyEventToAccelerator(e);
			if (accel) {
				onChange(accel);
				setRecording(false);
			}
		},
		[onChange, recording]
	);

	return (
		<div className="flex items-center justify-between gap-2.5 border-border/60 border-b py-3 last:border-b-0">
			<div className="flex items-center gap-2.5 min-w-0">
				<div className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
					<Icon className="size-3.5" />
				</div>
				<div className="min-w-0">
					<p className="text-sm font-medium text-foreground truncate">{label}</p>
					<p className="text-xs text-muted-foreground line-clamp-2">{description}</p>
				</div>
			</div>
			<div className="flex shrink-0 items-center gap-1">
				{!isDefault && (
					<Button
						variant="ghost"
						size="icon"
						className="size-7 text-muted-foreground hover:text-foreground"
						onClick={onReset}
						title="Reset to default"
					>
						<RotateCcw className="size-3" />
					</Button>
				)}
				<Button
					ref={inputRef}
					type="button"
					variant="ghost"
					title={recording ? "Press shortcut keys" : "Click to edit shortcut"}
					onClick={() => setRecording(true)}
					onKeyDown={handleKeyDown}
					onBlur={() => setRecording(false)}
					className={
						recording
							? "h-7 border border-transparent bg-primary/5 px-0 outline-none ring-0 focus:outline-none focus-visible:outline-none focus-visible:ring-0"
							: "h-7 cursor-pointer border border-transparent bg-transparent px-0 outline-none ring-0 transition-colors hover:bg-accent hover:text-accent-foreground focus:outline-none focus-visible:outline-none focus-visible:ring-0"
					}
				>
					{recording ? (
						<span className="px-2 text-[9px] text-primary whitespace-nowrap">Press hotkeys</span>
					) : (
						<ShortcutKbd keys={displayKeys} className="ml-0 px-1.5 text-foreground/85" />
					)}
				</Button>
			</div>
		</div>
	);
}

export default function DesktopLoginPage() {
	const router = useRouter();
	const api = useElectronAPI();

	const [email, setEmail] = useState("");
	const [password, setPassword] = useState("");
	const [showPassword, setShowPassword] = useState(false);
	const [loginError, setLoginError] = useState<string | null>(null);
	const [isLoggingIn, setIsLoggingIn] = useState(false);

	const [shortcuts, setShortcuts] = useState(DEFAULT_SHORTCUTS);
	const [shortcutsLoaded, setShortcutsLoaded] = useState(false);
	const isMac = api?.versions?.platform === "darwin";

	useEffect(() => {
		if (!api?.getShortcuts) {
			setShortcutsLoaded(true);
			return;
		}
		api
			.getShortcuts()
			.then((config: ShortcutMap | null) => {
				if (config) setShortcuts(config);
				setShortcutsLoaded(true);
			})
			.catch(() => setShortcutsLoaded(true));
	}, [api]);

	const updateShortcut = useCallback(
		(key: ShortcutKey, accelerator: string) => {
			setShortcuts((prev) => {
				const updated = { ...prev, [key]: accelerator };
				api?.setShortcuts?.({ [key]: accelerator }).catch(() => {
					toast.error("Failed to update shortcut");
				});
				return updated;
			});
			toast.success("Shortcut updated");
		},
		[api]
	);

	const resetShortcut = useCallback(
		(key: ShortcutKey) => {
			updateShortcut(key, DEFAULT_SHORTCUTS[key]);
		},
		[updateShortcut]
	);

	const handleLocalLogin = async (e: React.FormEvent) => {
		e.preventDefault();
		setLoginError(null);
		if (isLoggingIn) return;
		setIsLoggingIn(true);

		try {
			if (!api?.loginPassword) {
				throw new Error("Desktop password login is not available");
			}
			await api.loginPassword(email, password);

			setTimeout(() => {
				router.push(getPostLoginRedirectPath());
			}, 300);
		} catch (err) {
			if (err instanceof Error) {
				setLoginError(err.message);
			} else {
				setLoginError("Login failed. Please check your credentials.");
			}
		} finally {
			setIsLoggingIn(false);
		}
	};

	return (
		<div className="relative flex min-h-svh items-center justify-center bg-main-panel p-4 sm:p-6 select-none">
			<div className="relative flex w-full max-w-md flex-col overflow-hidden bg-main-panel">
				{/* Header */}
				<div className="flex flex-col items-center px-6 pt-6 pb-2 text-center">
					<Image
						src="/icon-128.svg"
						className="select-none dark:invert size-12 rounded-lg mb-3"
						alt={BRAND.name}
						width={48}
						height={48}
						priority
					/>
					<h1 className="text-lg font-semibold tracking-tight">Welcome to {BRAND.name} Desktop</h1>
					<p className="mt-1 text-sm text-muted-foreground">
						Configure shortcuts, then sign in to get started
					</p>
				</div>

				{/* Scrollable content */}
				<div className="flex-1 overflow-y-auto px-6 py-4">
					<div className="flex flex-col gap-5">
						{/* ---- Shortcuts ---- */}
						{shortcutsLoaded ? (
							<div className="flex flex-col gap-2">
								{/* <p className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
									Hotkeys
								</p> */}
								<div>
									{HOTKEY_ROWS.map((row) => (
										<HotkeyRow
											key={row.key}
											label={row.label}
											description={row.description}
											value={shortcuts[row.key]}
											defaultValue={DEFAULT_SHORTCUTS[row.key]}
											icon={row.icon}
											isMac={isMac}
											onChange={(accel) => updateShortcut(row.key, accel)}
											onReset={() => resetShortcut(row.key)}
										/>
									))}
								</div>
							</div>
						) : (
							<div className="flex justify-center py-6">
								<Spinner size="sm" />
							</div>
						)}

						<Separator />

						{/* ---- Auth ---- */}
						<div className="flex flex-col gap-3">
							{/* <p className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
								Sign In
							</p> */}

							<form onSubmit={handleLocalLogin} className="flex flex-col gap-3">
								{loginError && (
									<div className="rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">
										{loginError}
									</div>
								)}

								<div className="flex flex-col gap-1.5">
									<Label htmlFor="email" className="text-xs">
										Email
									</Label>
									<Input
										id="email"
										type="email"
										placeholder="you@example.com"
										required
										value={email}
										onChange={(e) => setEmail(e.target.value)}
										disabled={isLoggingIn}
										autoFocus
										className="h-9"
									/>
								</div>

								<div className="flex flex-col gap-1.5">
									<Label htmlFor="password" className="text-xs">
										Password
									</Label>
									<div className="relative">
										<Input
											id="password"
											type={showPassword ? "text" : "password"}
											placeholder="Enter your password"
											required
											value={password}
											onChange={(e) => setPassword(e.target.value)}
											disabled={isLoggingIn}
											className="h-9 pr-9"
										/>
										<Button
											type="button"
											variant="ghost"
											onClick={() => setShowPassword((v) => !v)}
											className="absolute inset-y-0 right-0 h-auto bg-transparent px-2.5 py-0 text-muted-foreground hover:bg-transparent hover:text-foreground"
											tabIndex={-1}
										>
											{showPassword ? (
												<EyeOff className="size-3.5" />
											) : (
												<Eye className="size-3.5" />
											)}
										</Button>
									</div>
								</div>

								<Button type="submit" disabled={isLoggingIn} className="relative h-9 mt-1">
									<span className={isLoggingIn ? "opacity-0" : ""}>Sign in</span>
									{isLoggingIn && (
										<Spinner size="sm" className="absolute text-primary-foreground" />
									)}
								</Button>
							</form>
						</div>
					</div>
				</div>
			</div>
		</div>
	);
}
