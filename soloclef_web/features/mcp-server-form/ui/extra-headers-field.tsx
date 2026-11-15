"use client";

import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { type FieldErrors, type HeaderRow, newRowId } from "../model/server-form-state";
import { FieldMessage } from "./field-message";
import { SecretInput } from "./secret-input";

interface ExtraHeadersFieldProps {
	rows: HeaderRow[];
	errors: FieldErrors;
	disabled?: boolean;
	onChange: (rows: HeaderRow[]) => void;
}

/**
 * Headers a server wants besides its sign-in (an organisation id, a
 * version). Each value is stored encrypted, like the key, so each is a
 * secret field.
 */
export function ExtraHeadersField({ rows, errors, disabled, onChange }: ExtraHeadersFieldProps) {
	const update = (id: string, part: Partial<HeaderRow>) =>
		onChange(rows.map((row) => (row.id === id ? { ...row, ...part } : row)));

	return (
		<div className="flex flex-col gap-2">
			<div className="flex items-center justify-between gap-2">
				<Label className="text-sm">Extra headers</Label>
				<Button
					type="button"
					variant="ghost"
					size="sm"
					className="h-7 gap-1 px-2 text-xs"
					disabled={disabled}
					onClick={() => onChange([...rows, { id: newRowId(), name: "", value: "" }])}
				>
					<Plus className="size-3.5" />
					Add header
				</Button>
			</div>
			{rows.length === 0 ? (
				<p className="text-xs text-muted-foreground">
					Optional. Most servers need only their sign-in.
				</p>
			) : (
				<ul className="flex flex-col gap-3">
					{rows.map((row, index) => {
						const nameError = errors[`row:${row.id}:name`];
						const valueError = errors[`row:${row.id}:value`];
						return (
							<li key={row.id} className="flex flex-col gap-1">
								<div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto]">
									<Input
										aria-label={`Header ${index + 1} name`}
										placeholder="Header name"
										value={row.name}
										disabled={disabled}
										aria-invalid={Boolean(nameError) || undefined}
										aria-describedby={nameError ? `${row.id}-name-error` : undefined}
										onChange={(event) => update(row.id, { name: event.target.value })}
										className="col-start-1"
									/>
									<div className="col-span-2 row-start-2 sm:col-span-1 sm:col-start-2 sm:row-start-1">
										<SecretInput
											id={`${row.id}-value`}
											label={`header ${index + 1} value`}
											placeholder="Value"
											value={row.value}
											disabled={disabled}
											invalid={Boolean(valueError)}
											describedBy={valueError ? `${row.id}-value-error` : undefined}
											onChange={(value) => update(row.id, { value })}
										/>
									</div>
									<Button
										type="button"
										variant="ghost"
										size="icon"
										className="col-start-2 row-start-1 size-9 text-muted-foreground sm:col-start-3"
										aria-label={`Remove header ${index + 1}`}
										disabled={disabled}
										onClick={() => onChange(rows.filter((other) => other.id !== row.id))}
									>
										<Trash2 className="size-4" />
									</Button>
								</div>
								<FieldMessage id={`${row.id}-name-error`} message={nameError} />
								<FieldMessage id={`${row.id}-value-error`} message={valueError} />
							</li>
						);
					})}
				</ul>
			)}
		</div>
	);
}
