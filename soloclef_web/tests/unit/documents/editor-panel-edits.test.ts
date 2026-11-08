import assert from "node:assert/strict";
import test from "node:test";
import { editAfterChange } from "@/components/editor-panel/edit-after-change";

const panel = { editing: true, loaded: true, saved: "# Memory\n\n- likes tea\n" };

test("the first edit the editor reports turns Save on", () => {
	// The editor reports the person's edits only, so the first report is a keystroke.
	assert.equal(editAfterChange("# Memory\n\n- likes tea!\n", panel), "# Memory\n\n- likes tea!\n");
});

test("back to the saved text, Save goes off; nothing counts before loading or outside editing", () => {
	assert.equal(editAfterChange(panel.saved, panel), null);
	assert.equal(editAfterChange("x", { ...panel, loaded: false }), undefined);
	assert.equal(editAfterChange("x", { ...panel, editing: false }), undefined);
});
