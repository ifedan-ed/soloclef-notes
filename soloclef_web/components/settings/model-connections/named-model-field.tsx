import { useId, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

/**
 * Adds a model to a new connection by the name its server knows it by. Shown
 * for a provider the model service has no list for; see ./model-listing.
 */
export function NamedModelField({ onAdd }: { onAdd: (name: string) => void }) {
	const id = useId();
	const [name, setName] = useState("");
	const trimmed = name.trim();

	function add() {
		if (!trimmed) return;
		onAdd(trimmed);
		setName("");
	}

	return (
		<div className="flex flex-col gap-2">
			<Label htmlFor={id}>Model name</Label>
			<div className="flex gap-2">
				<Input
					id={id}
					value={name}
					onChange={(event) => setName(event.target.value)}
					onKeyDown={(event) => {
						// Enter adds the model; it must not submit the dialog.
						if (event.key === "Enter") {
							event.preventDefault();
							add();
						}
					}}
					placeholder="The name the server lists the model under"
					autoComplete="off"
					spellCheck={false}
				/>
				<Button type="button" variant="secondary" onClick={add} disabled={!trimmed}>
					Add model
				</Button>
			</div>
			<p className="text-xs text-muted-foreground">
				Models at an address of your own are not listed for you. Add each one by name.
			</p>
		</div>
	);
}
