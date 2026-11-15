"use client";

import { Eye, EyeOff } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { isMasked, MASK } from "../model/server-form-state";

interface SecretInputProps {
	id: string;
	value: string;
	onChange: (value: string) => void;
	placeholder?: string;
	invalid?: boolean;
	describedBy?: string;
	disabled?: boolean;
	label: string;
}

/**
 * A key, a token or a header value. A saved one arrives masked and is shown
 * as saved, read-only, with Replace: typing into "••••abcd" would leave the
 * mask in front, and the server reads any value starting with the mask as
 * "keep the stored one", so the new text would be silently dropped.
 */
export function SecretInput({
	id,
	value,
	onChange,
	placeholder,
	invalid,
	describedBy,
	disabled,
	label,
}: SecretInputProps) {
	const [shown, setShown] = useState(false);
	if (isMasked(value)) {
		const ending = value.slice(MASK.length);
		return (
			<div className="flex items-center gap-2">
				<Input
					id={id}
					value={ending ? `Saved, ends in ${ending}` : "Saved"}
					readOnly
					aria-label={`${label}: saved`}
					className="bg-muted/40 text-muted-foreground"
				/>
				<Button
					type="button"
					variant="outline"
					size="sm"
					className="h-9 shrink-0"
					disabled={disabled}
					onClick={() => onChange("")}
				>
					Replace
				</Button>
			</div>
		);
	}
	return (
		<div className="relative">
			<Input
				id={id}
				type={shown ? "text" : "password"}
				autoComplete="off"
				spellCheck={false}
				value={value}
				placeholder={placeholder}
				disabled={disabled}
				aria-invalid={invalid || undefined}
				aria-describedby={describedBy}
				onChange={(event) => onChange(event.target.value)}
				className="pr-10"
			/>
			<Button
				type="button"
				variant="ghost"
				size="icon"
				className="absolute top-1/2 right-1 size-7 -translate-y-1/2 text-muted-foreground"
				aria-label={shown ? `Hide ${label}` : `Show ${label}`}
				onClick={() => setShown((now) => !now)}
			>
				{shown ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
			</Button>
		</div>
	);
}
