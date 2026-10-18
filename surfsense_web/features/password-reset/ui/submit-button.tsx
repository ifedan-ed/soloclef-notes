import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

/** A full-width submit whose label keeps its width while the spinner shows. */
export function SubmitButton({
	pending,
	children,
}: {
	pending: boolean;
	children: React.ReactNode;
}) {
	return (
		<Button
			type="submit"
			disabled={pending}
			className="relative h-auto w-full px-4 py-1.5 text-sm md:py-2 md:text-base"
		>
			<span className={pending ? "invisible" : ""}>{children}</span>
			{pending ? (
				<span className="absolute inset-0 flex items-center justify-center">
					<Spinner size="sm" className="text-primary-foreground" />
				</span>
			) : null}
		</Button>
	);
}
