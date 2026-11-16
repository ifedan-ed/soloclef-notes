---
name: xlsx
description: Create polished Excel workbooks for budgets, trackers, tables, and explicit .xlsx spreadsheet requests.
---

# XLSX

Create the requested workbook in `/workspace` with the preinstalled
`xlsxwriter` and `openpyxl` packages. Never install or download dependencies.
Use XlsxWriter for new workbooks because it can write explicit formula caches
that verification and the browser grid require.

## Authoring rules

- Prefer one clear sheet purpose per worksheet. Name sheets for the user, not
  `Sheet1` / `Sheet2`, unless the request is a blank template.
- Set column widths deliberately. Freeze header rows when tables are scrolled.
- Style headers once (bold, fill, borders) and reuse the style objects. Prefer
  number formats (`#,##0.00`, `0%`, dates) over pre-formatted strings.
- Write formulas as formulas (`worksheet.write_formula("B2", "=A2*1.1",
  cell_format)`), never as values you worked out yourself. `verify_artifact`
  recalculates the workbook with LibreOffice and saves the recalculated file,
  so every formula carries its computed value in the browser grid.
- Never leave Excel error literals (`#DIV/0!`, `#REF!`, `#VALUE!`, `#N/A`, …)
  as cached results. Fix the formula or the inputs instead.
- Keep workbooks under the verification cell ceiling (100 000 cells). Prefer
  compact tables over giant empty ranges.
- Charts, pivot tables, macros, and VBA are out of scope for this skill. Stick
  to values, formulas, formats, panes, and multiple sheets.

## Revisions

Start an in-place revision with `load_artifact_for_revision`. Open
`primary_path` with `openpyxl` using `data_only=False`, edit that current
workbook directly, and write the revision to `expected_output_path`. Preserve
unaffected worksheets, formulas, formatting, panes, validation, links, and
workbook settings. Use `markdown_path` as textual context, not as a replacement
workbook. Do not use vision to reconstruct workbook contents.

Save the verified revision with the returned `artifact_id` and
`expected_generation`. A changed title, filename, or design is still the same
artifact unless the user explicitly asks for a separate copy.

## Verify and save

Call `verify_artifact(path=output_path, format="xlsx")`. Verification first
recalculates every formula with LibreOffice and writes the recalculated file
over yours, then checks its structure: there is no PDF preview and no
placement check. A formula whose value is an Excel error is a blocker. Warnings are
advisory. If it reports blocking findings, fix all blockers together,
regenerate once at the same output path, and reverify. If a blocker remains,
stop and explain it instead of entering another automatic rewrite loop.

Then call `save_artifact(path=output_path, title="...",
markdown_representation="...")`. Working files may use any paths; no source
file, preview file, or matching filename stem is part of the publication
contract. The Markdown representation must faithfully summarize the workbook's
sheets, column meanings, and key figures for accessibility and search.
