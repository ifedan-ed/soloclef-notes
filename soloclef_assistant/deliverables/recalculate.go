package deliverables

import (
	"context"
	"strings"
	"time"
)

// # A workbook's formulas carry their values
//
// The page's grid, a phone and any reader without a spreadsheet program
// show a formula's cached result, never the formula. A workbook written by
// openpyxl has no cached results at all, and one written by XlsxWriter has
// whatever number the model's code passed beside the formula: nothing, or
// a value the model worked out itself, which can be wrong. So a data table
// showed empty cells where its rates were (=B6/B5), or numbers that did not
// follow from its own columns.
//
// Before a workbook is checked, it is opened and saved again by LibreOffice
// in the sandbox, told to recalculate every formula on load (by default it
// trusts the cached values of an Excel file). The saved file replaces the
// model's, so what is checked, hashed and stored is the recalculated one,
// and the structural check (checkXLSX) then holds every formula to having
// a value that is not an error. Sheets, widths, formats and frozen panes
// survive the round trip.

// markRecalculated is the line the recalculation prints when it replaced
// the file.
const markRecalculated = "SOLOCLEF_RECALCULATED=1"

// recalcTimeout bounds the recalculation: LibreOffice opens a workbook of
// the cell ceiling in seconds, and one that hangs must not hold the run.
const recalcTimeout = 2 * time.Minute

// recalcSettings is a LibreOffice profile's settings file that makes it
// recalculate an Excel 2007+ file's formulas on load (OOXMLRecalcMode 0,
// "always"), whatever values the file carries.
const recalcSettings = `<?xml version="1.0" encoding="UTF-8"?>
<oor:items xmlns:oor="http://openoffice.org/2001/registry" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><item oor:path="/org.openoffice.Office.Calc/Formula/Load"><prop oor:name="OOXMLRecalcMode" oor:op="fuse"><value>0</value></prop></item></oor:items>`

// recalcCommand recalculates the workbook at source in place. It works in
// a directory of its own, with a LibreOffice profile of its own (two
// conversions sharing one lock each other out), converts a copy so that
// LibreOffice never writes over its own input, and only then replaces the
// file. Every failure prints the step it failed at, as the render command's
// do, and removes the directory.
func recalcCommand(dir, source string) string {
	var b strings.Builder
	b.WriteString("dir=" + quoted(dir) + "; src=" + quoted(source) + "; copy=\"$dir/workbook.xlsx\"\n")
	b.WriteString(`fail() { echo "` + markFailed + `$1"; rm -rf -- "$dir"; exit 0; }` + "\n")
	b.WriteString(`mkdir -p -- "$dir/profile/user" "$dir/out" && cp -- "$src" "$copy" || fail copying` + "\n")
	b.WriteString(`printf '%s' ` + quoted(recalcSettings) + ` > "$dir/profile/user/registrymodifications.xcu" || fail copying` + "\n")
	b.WriteString(`soffice --headless "-env:UserInstallation=file://$dir/profile" --convert-to 'xlsx:Calc MS Excel 2007 XML' --outdir "$dir/out" "$copy" >/dev/null 2>&1` + "\n")
	b.WriteString(`test -s "$dir/out/workbook.xlsx" || fail recalculating` + "\n")
	b.WriteString(`cp -- "$dir/out/workbook.xlsx" "$src" || fail replacing` + "\n")
	b.WriteString(`rm -rf -- "$dir"` + "\n")
	b.WriteString(`echo "` + markRecalculated + `"` + "\n")
	return b.String()
}

// recalculate runs recalcCommand on the file. It answers a finding for the
// model when the workbook could not be recalculated ("" when it was), and
// an error only when the sandbox itself failed.
func (s Specialist) recalculate(ctx context.Context, bench Bench, where string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, recalcTimeout)
	defer cancel()
	ran, err := bench.Run(ctx, recalcCommand("/tmp/soloclef-recalc-"+s.Saver.NewID(), where))
	if err != nil {
		return "", err
	}
	switch {
	case ran.TimedOut:
		return "Recalculating the workbook took too long; make it smaller or simpler.", nil
	case strings.Contains(ran.Output, markFailed+"recalculating"):
		return "LibreOffice could not open the workbook to recalculate its formulas; the file is not a valid xlsx workbook.", nil
	case strings.Contains(ran.Output, markFailed+"copying"):
		return "The artifact file is missing. Generate it again before verification.", nil
	case !strings.Contains(ran.Output, markRecalculated):
		return "The recalculated workbook could not be written back; verify it again.", nil
	}
	return "", nil
}
