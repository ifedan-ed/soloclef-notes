import { Eye, EyeOff, Plus, X } from "lucide-react";
import type { ReactNode } from "react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import type { HeaderRow } from "./connection-headers";

interface ApiBaseUrlFieldProps {
	value: string;
	onChange: (value: string) => void;
	/** Placeholder, typically the provider's prefilled default base URL. */
	placeholder?: string;
	hint?: ReactNode;
}

/** Shared API Base URL input. The prefilled default is passed in via `value`. */
export function ApiBaseUrlField({ value, onChange, placeholder, hint }: ApiBaseUrlFieldProps) {
	return (
		<div className="flex flex-col gap-2">
			<Label>API Base URL</Label>
			<Input
				value={value}
				onChange={(event) => onChange(event.target.value)}
				placeholder={placeholder || "https://api.example.com/v1"}
			/>
			{hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
		</div>
	);
}

interface ApiKeyFieldProps {
	value: string;
	onChange: (value: string) => void;
	label?: string;
	placeholder?: string;
}

/** Shared masked API Key input. */
export function ApiKeyField({
	value,
	onChange,
	label = "API Key",
	placeholder = "API key",
}: ApiKeyFieldProps) {
	const [showApiKey, setShowApiKey] = useState(false);

	return (
		<div className="flex flex-col gap-2">
			<Label>{label}</Label>
			<div className="relative">
				<Input
					value={value}
					onChange={(event) => onChange(event.target.value)}
					placeholder={placeholder}
					type={showApiKey ? "text" : "password"}
					className="pr-11"
				/>
				<Button
					type="button"
					variant="ghost"
					size="icon"
					className="absolute top-1/2 right-1 size-8 -translate-y-1/2 text-muted-foreground"
					onClick={() => setShowApiKey((current) => !current)}
					disabled={!value}
					aria-label={showApiKey ? "Hide API key" : "Show API key"}
				>
					{showApiKey ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
				</Button>
			</div>
		</div>
	);
}

interface HeadersFieldProps {
	rows: HeaderRow[];
	onChange: (rows: HeaderRow[]) => void;
	/** The sentence of what is wrong with the rows, or null. */
	problem: string | null;
	hint?: ReactNode;
}

/**
 * Extra headers for an OpenAI-compatible endpoint, as name and value rows.
 * The values are typed like a key (masked), because a gateway's header is
 * often one. There is always at least one row to type into.
 */
export function HeadersField({ rows, onChange, problem, hint }: HeadersFieldProps) {
	const shown = rows.length > 0 ? rows : [{ name: "", value: "" }];
	function change(index: number, next: Partial<HeaderRow>) {
		onChange(shown.map((row, at) => (at === index ? { ...row, ...next } : row)));
	}
	return (
		<div className="flex flex-col gap-2">
			<Label>Extra headers (optional)</Label>
			{shown.map((row, index) => (
				// Rows have no identity but their place, and are only ever
				// added at the end or removed by their own button.
				// biome-ignore lint/suspicious/noArrayIndexKey: see above
				<div key={index} className="flex flex-col gap-2 sm:flex-row">
					<Input
						value={row.name}
						onChange={(event) => change(index, { name: event.target.value })}
						placeholder="Header name"
						aria-label={`Header ${index + 1} name`}
						autoComplete="off"
						spellCheck={false}
						className="sm:w-2/5"
					/>
					<div className="flex min-w-0 flex-1 gap-2">
						<Input
							value={row.value}
							onChange={(event) => change(index, { value: event.target.value })}
							placeholder="Value"
							aria-label={`Header ${index + 1} value`}
							type="password"
							autoComplete="new-password"
							data-1p-ignore
							data-lpignore="true"
						/>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							className="shrink-0 text-muted-foreground"
							onClick={() => onChange(shown.filter((_, at) => at !== index))}
							aria-label={`Remove header ${index + 1}`}
							disabled={shown.length === 1 && !row.name && !row.value}
						>
							<X className="h-4 w-4" />
						</Button>
					</div>
				</div>
			))}
			<div>
				<Button
					type="button"
					variant="ghost"
					size="sm"
					className="h-8 px-2 text-muted-foreground"
					onClick={() => onChange([...shown, { name: "", value: "" }])}
				>
					<Plus className="h-4 w-4" /> Add header
				</Button>
			</div>
			{problem ? (
				<p role="alert" className="text-xs text-destructive">
					{problem}
				</p>
			) : hint ? (
				<p className="text-xs text-muted-foreground">{hint}</p>
			) : null}
		</div>
	);
}

interface ConnectFormFooterProps {
	onCancel: () => void;
	onSubmit: () => void;
	canSubmit: boolean;
	isPending: boolean;
}

/** Shared Cancel / Connect footer for every provider connect form. */
export function ConnectFormFooter({
	onCancel,
	onSubmit,
	canSubmit,
	isPending,
}: ConnectFormFooterProps) {
	return (
		<DialogFooter className="shrink-0 border-t bg-popover px-6 py-4">
			<Button variant="secondary" onClick={onCancel}>
				Cancel
			</Button>
			<Button
				onClick={onSubmit}
				disabled={isPending || !canSubmit}
				className="relative min-w-[96px]"
			>
				<span className={isPending ? "opacity-0" : ""}>Connect</span>
				{isPending ? <Spinner size="sm" className="absolute" /> : null}
			</Button>
		</DialogFooter>
	);
}
