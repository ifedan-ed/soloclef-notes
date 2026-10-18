import type { ReactNode } from "react";

/** The title row every billing section shares, with room for an action on the right. */
export function SectionHeading({
	title,
	description,
	action,
}: {
	title: string;
	description?: string;
	action?: ReactNode;
}) {
	return (
		<div className="flex flex-wrap items-end justify-between gap-3">
			<div className="min-w-0">
				<h2 className="text-base font-semibold">{title}</h2>
				{description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
			</div>
			{action}
		</div>
	);
}
