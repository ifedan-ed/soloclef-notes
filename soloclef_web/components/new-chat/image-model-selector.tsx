"use client";

import { Check, ChevronDown, Search, SlidersHorizontal } from "lucide-react";
import { useRouter } from "next/navigation";
import type { UIEvent } from "react";
import { useCallback, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Drawer,
	DrawerContent,
	DrawerHandle,
	DrawerHeader,
	DrawerTitle,
	DrawerTrigger,
} from "@/components/ui/drawer";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Spinner } from "@/components/ui/spinner";
import { ModelFamilyIcon } from "@/features/chat-model/model-family-icon";
import { matchingOptions, optionLabel } from "@/features/chat-model/model-option";
import { ModelOptionText } from "@/features/chat-model/ui/model-option-text";
import { defaultLabel } from "@/features/model-choice/model-choice";
import { useChooseModelRole, useModelRoles } from "@/features/model-choice/use-model-choice";
import { useIsMobile } from "@/hooks/use-mobile";
import { settingsPath } from "@/lib/dashboard-paths";
import { AUTO_PROVIDER_ICON_KEY, getProviderIcon } from "@/lib/provider-icons";
import { cn } from "@/lib/utils";

interface ImageModelSelectorProps {
	className?: string;
	mobileIconOnly?: boolean;
}

/**
 * The account's image model, in the chat header: the same setting as the
 * model page's (GET and PUT /api/v1/users/me/model-roles), chosen by the
 * id the server lists the model under (a proxy name, or one of the account's
 * own connections' models).
 */
export function ImageModelSelector({ className, mobileIconOnly = false }: ImageModelSelectorProps) {
	const router = useRouter();
	const isMobile = useIsMobile();
	const [open, setOpen] = useState(false);
	const [search, setSearch] = useState("");
	const [scrollPos, setScrollPos] = useState<"top" | "middle" | "bottom">("top");
	const roles = useModelRoles();
	const choose = useChooseModelRole("image");

	const image = roles.data?.image;
	const options = useMemo(() => image?.models ?? [], [image]);
	const visibleOptions = useMemo(() => matchingOptions(options, search), [options, search]);
	const selected = image?.selected ?? null;
	// An own model's id is a number, so the trigger shows its label.
	const selectedLabel = selected === null ? null : optionLabel(options, selected);
	const loading = roles.isLoading;
	const hasSearchQuery = search.trim().length > 0;
	const showIconOnlyTrigger = isMobile && mobileIconOnly;

	function handleOpenChange(nextOpen: boolean) {
		if (!nextOpen) setSearch("");
		setOpen(nextOpen);
	}

	function selectModel(name: string | null) {
		choose.mutate(name);
		setSearch("");
		setOpen(false);
	}

	function manageModelConnections() {
		setOpen(false);
		router.push(settingsPath("models"));
	}

	const handleScroll = useCallback((event: UIEvent<HTMLDivElement>) => {
		const el = event.currentTarget;
		const atTop = el.scrollTop <= 2;
		const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight <= 2;
		setScrollPos(atTop ? "top" : atBottom ? "bottom" : "middle");
	}, []);

	// Only surface this control when the server offers a model that draws.
	if (!loading && options.length === 0) {
		return null;
	}

	const content = (
		<div className="flex h-[320px] select-none flex-col overflow-hidden">
			<div className="p-2">
				<div className="relative">
					<Search className="absolute left-0.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
					<Input
						value={search}
						onChange={(event) => setSearch(event.target.value)}
						placeholder="Search image models"
						className="h-8 border-0 bg-transparent pl-6 text-sm shadow-none"
					/>
				</div>
			</div>
			<div
				className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-1.5 py-1.5"
				onScroll={handleScroll}
				style={{
					maskImage: `linear-gradient(to bottom, ${scrollPos === "top" ? "black" : "transparent"}, black 16px, black calc(100% - 16px), ${scrollPos === "bottom" ? "black" : "transparent"})`,
					WebkitMaskImage: `linear-gradient(to bottom, ${scrollPos === "top" ? "black" : "transparent"}, black 16px, black calc(100% - 16px), ${scrollPos === "bottom" ? "black" : "transparent"})`,
				}}
			>
				<button
					type="button"
					className="flex w-full items-center justify-between rounded-md px-3 py-2 text-left transition-colors hover:bg-accent hover:text-accent-foreground"
					onClick={() => selectModel(null)}
				>
					<div className="min-w-0 flex-1">
						<div className="flex min-w-0 items-center gap-2 font-medium">
							{getProviderIcon(AUTO_PROVIDER_ICON_KEY, { className: "size-4 shrink-0" })}
							<span className="truncate">{image ? defaultLabel(image) : "Server default"}</span>
						</div>
					</div>
					{selected === null ? <Check className="h-4 w-4" /> : null}
				</button>
				{loading ? (
					<div className="flex items-center justify-center py-8">
						<Spinner />
					</div>
				) : visibleOptions.length === 0 ? (
					<div className="px-3 py-8 text-center text-sm text-muted-foreground">
						{hasSearchQuery ? "No matching image models." : "The server offers no image models."}
					</div>
				) : (
					visibleOptions.map((option) => (
						<button
							type="button"
							key={option.id}
							className="flex w-full items-center justify-between rounded-md px-3 py-2 text-left transition-colors hover:bg-accent hover:text-accent-foreground"
							onClick={() => selectModel(option.id)}
						>
							<div className="flex min-w-0 flex-1 items-center gap-2">
								<ModelFamilyIcon name={option.label} className="size-4 shrink-0" />
								<ModelOptionText option={option} />
							</div>
							<div className="ml-3 flex shrink-0 items-center gap-2">
								{selected === option.id ? <Check className="h-4 w-4" /> : null}
							</div>
						</button>
					))
				)}
			</div>
			{/* Every account may add its own connection, so the way to the
			    model settings is offered to everyone. */}
			<div className="p-2">
				<Button
					variant="ghost"
					className="w-full justify-start rounded-md bg-foreground/5 hover:bg-foreground/10 hover:text-foreground"
					onClick={manageModelConnections}
				>
					<SlidersHorizontal className="h-4 w-4" /> Manage models
				</Button>
			</div>
		</div>
	);

	const trigger = (
		<Button
			type="button"
			variant="ghost"
			size="sm"
			data-desktop-layout-overflow-boundary
			aria-label="Select image model"
			className={cn(
				"h-8 min-w-0 gap-2 rounded-md px-3 text-muted-foreground transition-colors",
				"select-none",
				"hover:bg-foreground/10 hover:text-foreground",
				"data-[state=open]:bg-foreground/10 data-[state=open]:text-foreground",
				className,
				showIconOnlyTrigger && "h-9 w-auto shrink-0 justify-center gap-1 px-2"
			)}
		>
			{selectedLabel ? (
				<ModelFamilyIcon name={selectedLabel} className="size-4 shrink-0" />
			) : (
				getProviderIcon(AUTO_PROVIDER_ICON_KEY, { className: "size-4 shrink-0" })
			)}
			{showIconOnlyTrigger ? null : (
				<span className="min-w-0 max-w-40 flex-1 truncate text-sm">
					{selectedLabel ?? "Default"}
				</span>
			)}
			<ChevronDown className="h-3.5 w-3.5 shrink-0" />
		</Button>
	);

	if (isMobile) {
		return (
			<Drawer open={open} onOpenChange={handleOpenChange}>
				<DrawerTrigger asChild>{trigger}</DrawerTrigger>
				<DrawerContent className="max-h-[85vh]">
					<DrawerHandle />
					<DrawerHeader>
						<DrawerTitle>Select Image Model</DrawerTitle>
					</DrawerHeader>
					{content}
				</DrawerContent>
			</Drawer>
		);
	}

	return (
		<Popover open={open} onOpenChange={handleOpenChange}>
			<PopoverTrigger asChild>{trigger}</PopoverTrigger>
			<PopoverContent
				align="start"
				className="w-[340px] border border-popover-border bg-popover p-0 text-popover-foreground shadow-md"
			>
				{content}
			</PopoverContent>
		</Popover>
	);
}
