"use client";
import { ChevronDown, NotebookPen } from "lucide-react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useNotebooks } from "@/features/notebooks/hooks/use-notebook-queries";
import { createViaChatPath, newestFirst } from "@/lib/automations/create-via-chat";
import { NOTEBOOKS_HOME } from "@/lib/dashboard-paths";

/** How many notebooks the menu lists: the newest, which is where a request is usually made. */
const LISTED = 8;

/**
 * "Create via chat": a menu of the person's notebooks, newest first. The
 * chosen one opens with "Create an automation that " typed in its chat,
 * where the assistant asks what it needs and drafts the automation onto a
 * card (lib/automations/create-via-chat.ts says why a notebook is chosen).
 */
export function CreateViaChatMenu({ size = "sm" }: { size?: "sm" | "default" }) {
	const { data, isLoading } = useNotebooks();
	const notebooks = newestFirst(data ?? []);
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button size={size} data-testid="create-via-chat">
					Create via chat
					<ChevronDown className="ml-1 h-4 w-4 opacity-70" aria-hidden />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-72 max-w-[calc(100vw-2rem)]">
				<DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
					Describe it in a notebook's chat
				</DropdownMenuLabel>
				<DropdownMenuSeparator />
				{isLoading ? (
					<DropdownMenuItem disabled>Loading notebooks…</DropdownMenuItem>
				) : notebooks.length === 0 ? (
					<DropdownMenuItem asChild>
						<Link href={NOTEBOOKS_HOME}>Make a notebook first</Link>
					</DropdownMenuItem>
				) : (
					notebooks.slice(0, LISTED).map((notebook) => (
						<DropdownMenuItem key={notebook.id} asChild>
							<Link href={createViaChatPath(notebook.id)} className="min-w-0">
								<NotebookPen className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden />
								<span className="truncate">{notebook.title || "Untitled notebook"}</span>
							</Link>
						</DropdownMenuItem>
					))
				)}
				{notebooks.length > LISTED ? (
					<>
						<DropdownMenuSeparator />
						<DropdownMenuItem asChild>
							<Link href={NOTEBOOKS_HOME}>All notebooks</Link>
						</DropdownMenuItem>
					</>
				) : null}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
