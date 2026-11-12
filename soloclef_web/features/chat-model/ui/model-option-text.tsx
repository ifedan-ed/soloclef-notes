import { type ModelOption, ownMark } from "../model-option";

/**
 * A menu entry's words: the model's label, and for a model of the account's
 * own connection a second line saying so. Every model menu draws its rows
 * with this, so a model the server calls directly at the person's own
 * endpoint is never mistaken for one of the server's.
 */
export function ModelOptionText({ option }: { option: ModelOption }) {
	const mark = ownMark(option);
	return (
		<span className="min-w-0 flex-1">
			<span className="block truncate font-medium" title={option.label}>
				{option.label}
			</span>
			{mark ? (
				<span className="block truncate text-xs text-muted-foreground" title={mark}>
					{mark}
				</span>
			) : null}
		</span>
	);
}
