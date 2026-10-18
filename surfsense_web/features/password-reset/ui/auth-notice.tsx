import { cn } from "@/lib/utils";

/** An inline message on the auth pages: an error, or a confirmation. */
export function AuthNotice({
	tone,
	title,
	children,
}: {
	tone: "error" | "success";
	title?: string;
	children: React.ReactNode;
}) {
	return (
		<div
			role={tone === "error" ? "alert" : "status"}
			className={cn(
				"rounded-md border p-4 text-left text-sm shadow-sm",
				tone === "error"
					? "border-destructive/30 bg-destructive/10 text-destructive"
					: "border-primary/30 bg-primary/10 text-foreground"
			)}
		>
			{title ? <p className="mb-1 font-semibold">{title}</p> : null}
			<div>{children}</div>
		</div>
	);
}

export const authInputClass =
	"mt-1 block w-full rounded-md border border-border bg-background px-3 py-1.5 text-foreground shadow-sm transition-all focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary/40 md:py-2";
