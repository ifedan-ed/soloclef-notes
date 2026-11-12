package deliverables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// The five kinds built by code. A report, a Word document, a slide deck, a
// workbook and an interactive page are not written by the model as text:
// the model writes a program (WeasyPrint, python-docx, python-pptx,
// openpyxl) and runs it in the thread's sandbox, and the file that program
// leaves behind is the artifact. The path is always the same four steps:
//
//  1. load_artifact_instructions reads the kind's skill out of the sandbox
//     image, where it lives beside the libraries it describes.
//  2. execute runs the model's code. The sandbox has no network.
//  3. verify_artifact reads the file out, checks its structure in this
//     process, has the sandbox render it (LibreOffice to PDF, poppler to a
//     list of every drawn word and its box) and holds that drawing to the
//     placement rules (verify.go, layout_rules.go).
//  4. save_artifact stores the bytes that passed, and for a Word document
//     or a deck the PDF rendering as its preview, which is what the page's
//     viewer shows.
//
// # What this does better than the tools it replaces
//
//   - What "verified" means is kept by the server (Verifications), keyed by
//     sandbox and path, with the file's hash. It used to be a signed file
//     written into the sandbox next to the model's own code, with a second
//     signed file for the preview's hash and a third tool trusted to read
//     them back.
//   - The structural checks run here, on bytes read out, never in the
//     sandbox where the code under test also runs.
//   - A sandbox is found by a label on the server, so any replica serves
//     any turn, and an account's limit holds across replicas (package
//     sandbox).
//   - One command renders, counts and measures; the checks used to be a
//     dozen round trips, each of which the sandbox daemon holds open for a
//     second after the command ends.
//   - Output shown to the model is cut at a character, not in the middle of
//     one.
const (
	FormatPDF  = "pdf"
	FormatDOCX = "docx"
	FormatPPTX = "pptx"
	FormatXLSX = "xlsx"
	FormatHTML = "html"
)

// The tools' names. verify_artifact is keyed to the page's verification
// card; the others show as steps.
const (
	ExecuteTool  = "execute"
	ReadFileTool = "read_sandbox_file"
	VerifyTool   = "verify_artifact"
)

// documentFormat is how one built format is checked and stored.
type documentFormat struct {
	// suffix is the file's extension, which the path must end in: the
	// format is declared, and a file whose name says otherwise is refused
	// rather than stored under a type it is not.
	suffix string
	mime   string
	// check is the structural check, on the file's bytes.
	check func([]byte) structure
	// rendered says the pages are rendered and their placement measured. A
	// workbook and an interactive page are not: a grid has no page to
	// judge, and a page that runs scripts lays out in the viewer.
	rendered bool
	// converted says LibreOffice makes the PDF the pages are rendered
	// from, and that this PDF is kept as the artifact's preview.
	converted bool
	// slides says each page is a slide: its shapes held to the deck's
	// placement rules, expected to be exactly as many as the deck declares,
	// and allowed to hold no text.
	slides bool
}

// documentFormats is the built formats by name.
var documentFormats = map[string]documentFormat{
	FormatPDF:  {suffix: ".pdf", mime: "application/pdf", check: checkPDFBytes, rendered: true},
	FormatDOCX: {suffix: ".docx", mime: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", check: checkDOCX, rendered: true, converted: true},
	FormatPPTX: {suffix: ".pptx", mime: "application/vnd.openxmlformats-officedocument.presentationml.presentation", check: checkPPTX, rendered: true, converted: true, slides: true},
	FormatXLSX: {suffix: ".xlsx", mime: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", check: checkXLSX},
	FormatHTML: {suffix: ".html", mime: "text/html", check: checkHTML},
}

// documentFormatNames is the built formats in the order a sentence lists
// them.
const documentFormatNames = "pdf, docx, pptx, xlsx or html"

// MaxDocumentBytes is the largest file a built artifact may be. It is the
// limit the rest of the application puts on an artifact's file; a document
// larger than this is one the page could not open comfortably either.
const MaxDocumentBytes = 30 << 20

// Bench is a thread's sandbox, as this package uses one.
type Bench interface {
	// ID names the sandbox. A verification belongs to one sandbox: a file
	// at the same path in another is another file.
	ID() string
	// Run runs a shell command. A command that fails is a Result; an error
	// is the sandbox failing.
	Run(ctx context.Context, command string) (sandbox.Result, error)
	// ReadFile reads a file, or answers sandbox.ErrNotFound or
	// sandbox.ErrTooLarge.
	ReadFile(ctx context.Context, path string, limit int64) ([]byte, error)
	WriteFile(ctx context.Context, path string, data []byte) error
}

// Workshop gives a turn its thread's sandbox.
type Workshop interface {
	// Check reports what is missing. Called once at start-up.
	Check() error
	// Bench returns the thread's sandbox, making it if the thread has
	// none. A *WorkshopRefusal is a reason the person can be told.
	Bench(ctx context.Context, owner Owner) (Bench, error)
}

// WorkshopRefusal is why a sandbox cannot be had, in words for the person.
type WorkshopRefusal struct{ Reason string }

func (r *WorkshopRefusal) Error() string { return r.Reason }

// The refusals' sentences.
const (
	noSandboxSentence   = "This deployment has no code sandbox, so reports, Word documents, slide decks, spreadsheets and interactive pages cannot be made here."
	sandboxFullSentence = "Your other conversations are using all of your sandboxes. Try again in a few minutes."
)

// NoSandbox is the Workshop of a deployment that has no sandbox server,
// chosen on purpose. Every built kind answers with the sentence saying so.
type NoSandbox struct{}

func (NoSandbox) Check() error { return nil }
func (NoSandbox) Bench(context.Context, Owner) (Bench, error) {
	return nil, &WorkshopRefusal{Reason: noSandboxSentence}
}

// Sandboxes is the Workshop over the OpenSandbox server.
type Sandboxes struct {
	Client *sandbox.Client
}

func (s Sandboxes) Check() error {
	if s.Client == nil {
		return errors.New("deliverables: Sandboxes.Client is not set")
	}
	return nil
}

func (s Sandboxes) Bench(ctx context.Context, owner Owner) (Bench, error) {
	session, err := s.Client.ForThread(ctx, owner.UserID, owner.ThreadID)
	if errors.Is(err, sandbox.ErrAccountFull) {
		return nil, &WorkshopRefusal{Reason: sandboxFullSentence}
	}
	if err != nil {
		return nil, err
	}
	return sessionBench{session}, nil
}

// sessionBench is a sandbox session as a Bench.
type sessionBench struct{ *sandbox.Session }

func (b sessionBench) ID() string { return b.Session.ID }

// refusedDocumentNote follows a refusal's sentence in what the model reads.
const refusedDocumentNote = " Nothing was made; tell the requester exactly that."

// bench returns the turn's sandbox, or the error the model is shown. The
// cause of a failure can name hosts and keys, so it is logged and the model
// gets a sentence it can pass on.
func (s Specialist) bench(ctx context.Context, owner Owner) (Bench, error) {
	bench, err := s.Workshop.Bench(ctx, owner)
	if err == nil {
		return bench, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var refused *WorkshopRefusal
	if errors.As(err, &refused) {
		return nil, errors.New(refused.Reason + refusedDocumentNote)
	}
	s.Saver.Logger.LogAttrs(ctx, slog.LevelError, "a thread's sandbox could not be had",
		slog.String("error", err.Error()), slog.String("user_id", owner.UserID), slog.Int64("thread_id", owner.ThreadID))
	return nil, errors.New("the code sandbox could not be reached, so nothing was made; tell the requester to try again shortly")
}

// sandboxFailure words an error from a sandbox call for the model.
func (s Specialist) sandboxFailure(ctx context.Context, owner Owner, doing string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, sandbox.ErrGone) {
		// Its files went with it, so the model must start again, not retry.
		return errors.New("the sandbox expired and its files are gone; build the artifact again from the start")
	}
	s.Saver.Logger.LogAttrs(ctx, slog.LevelError, "a sandbox call failed", slog.String("doing", doing),
		slog.String("error", err.Error()), slog.String("user_id", owner.UserID), slog.Int64("thread_id", owner.ThreadID))
	return errors.New("the sandbox failed while " + doing + "; try once more, and if it fails again stop and report it")
}

// quoted is text as one word of a shell command.
func quoted(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

// sandboxPath checks a path the model gave: absolute, already clean, one
// line. It is passed to a shell quoted, so this is not what keeps a command
// safe; it is what keeps "the file that was verified" and "the file that is
// saved" from being two spellings of different files.
func sandboxPath(written string) (string, error) {
	switch {
	case written == "":
		return "", errors.New("path is missing")
	case !utf8.ValidString(written) || strings.ContainsAny(written, "\x00\r\n"):
		return "", errors.New("path must be one line of text")
	case !path.IsAbs(written):
		return "", fmt.Errorf("path must be absolute, like /workspace/report.pdf; got %q", written)
	case path.Clean(written) != written:
		return "", fmt.Errorf("path must be written plainly, without . or .. or doubled slashes; got %q", written)
	}
	return written, nil
}

// skillBytes bounds a skill read from the image.
const skillBytes = 64 << 10

// documentNote follows a built kind's skill. The skills are the sandbox
// image's and also serve the tier this one replaces, so they name two tools
// this specialist does not have and a save without a format.
func documentNote(format string) string {
	return "\n\n---\nIn this application:\n" +
		"- Your tools are " + ExecuteTool + " (python or bash), " + ReadFileTool + ", " + VerifyTool + " and " + SaveTool + ".\n" +
		"- Save with " + SaveTool + `(format="` + format + `", title="...", path=output_path, markdown_representation="..."). The format is required.` + "\n" +
		"- There is no load_source_document and no load_artifact_for_revision. Always build a new artifact from the material in the task. If the task is to convert a file the user already has, or to revise an earlier artifact in place, make nothing and say that this cannot be done yet.\n" +
		"- Each python call is a new process in /workspace: carry state between calls in files."
}

// documentInstructions reads a built kind's skill from the thread's sandbox.
func (s Specialist) documentInstructions(ctx context.Context, owner Owner, format string) (string, error) {
	bench, err := s.bench(ctx, owner)
	if err != nil {
		return "", err
	}
	skill, err := bench.ReadFile(ctx, "/opt/skills/"+format+"/SKILL.md", skillBytes)
	if errors.Is(err, sandbox.ErrNotFound) || errors.Is(err, sandbox.ErrTooLarge) || (err == nil && strings.TrimSpace(string(skill)) == "") {
		// The image is the deployment's; one without the skill cannot make
		// this kind, and guessing at instructions would make it badly.
		s.Saver.Logger.LogAttrs(ctx, slog.LevelError, "the sandbox image has no usable skill for a kind", slog.String("format", format))
		return "", errors.New("the sandbox on this deployment has no instructions for " + format + ", so it cannot be made here." + refusedDocumentNote)
	}
	if err != nil {
		return "", s.sandboxFailure(ctx, owner, "loading the instructions", err)
	}
	return string(skill) + documentNote(format), nil
}

// maxShownOutput is how much of a command's output the model is shown. The
// rest is kept in a file in the sandbox, which the model can read in parts.
const maxShownOutput = 16_000

func (s Specialist) executeTool(owner Owner) tool.Tool {
	return tool.Tool{
		Name:        ExecuteTool,
		Description: "Run Python or a Bash command in the sandbox, to build a pdf, docx, pptx, xlsx or html artifact. Each call is a new process in /workspace; carry state between calls in files. The sandbox has no network. It answers the output and the exit code.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"code_or_command":{"type":"string","description":"Python source, or a Bash command line."},"language":{"type":"string","enum":["python","bash"],"description":"python unless stated."}},"required":["code_or_command"]}`),
		// The code runs in a container of the thread's own with no network
		// and nothing of the user's in it but what this task wrote, and it
		// is the only way these kinds are made. It does not ask.
		Approval: tool.Auto,
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			// Unknown members are let through for this tool and the two
			// below: their instructions come from the sandbox image, and a
			// skill that has the model add "description" must not fail it.
			var args struct {
				Code     string `json:"code_or_command"`
				Language string `json:"language"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return "", fmt.Errorf("the arguments are not readable: %w", err)
			}
			if strings.TrimSpace(args.Code) == "" {
				return "", errors.New("code_or_command is empty")
			}
			bench, err := s.bench(ctx, owner)
			if err != nil {
				return "", err
			}
			var result sandbox.Result
			switch strings.ToLower(strings.TrimSpace(args.Language)) {
			case "", "python":
				result, err = s.runPython(ctx, bench, args.Code)
			case "bash":
				result, err = bench.Run(ctx, "cd -- /workspace && "+args.Code)
			default:
				return "", fmt.Errorf("language %q is not supported; use python or bash", args.Language)
			}
			if err != nil {
				return "", s.sandboxFailure(ctx, owner, "running the code", err)
			}
			return s.shownOutput(ctx, bench, result), nil
		},
	}
}

// runPython writes the code to a file and runs it as one process. A file,
// not "python -c": the code is long, has every kind of quote in it, and a
// traceback should name line numbers of something.
func (s Specialist) runPython(ctx context.Context, bench Bench, code string) (sandbox.Result, error) {
	script := "/tmp/.soloclef-exec-" + s.Saver.NewID() + ".py"
	if err := bench.WriteFile(ctx, script, []byte(code)); err != nil {
		return sandbox.Result{}, err
	}
	// The trap removes the script however the run ends. The image's
	// environment script puts the chosen Python and its libraries on the
	// path.
	return bench.Run(ctx, "script="+quoted(script)+`; trap 'rm -f -- "$script"' EXIT; cd -- /workspace && `+
		`. /opt/code-interpreter/code-interpreter-env.sh python "${PYTHON_VERSION:-3.12}" >/dev/null 2>&1 && python3 "$script"`)
}

// shownOutput is a command's result as the model reads it.
func (s Specialist) shownOutput(ctx context.Context, bench Bench, result sandbox.Result) string {
	output := result.Output
	var notes []string
	if utf8.RuneCountInString(output) > maxShownOutput {
		// The whole of it is kept where the model can read the part it
		// needs. If it cannot be kept the model is simply not told of a
		// file: the cut is still marked.
		full := "/tmp/soloclef-output-" + s.Saver.NewID() + ".txt"
		if bench.WriteFile(ctx, full, []byte(output)) == nil {
			notes = append(notes, "[Full output: "+full+"]")
		}
		output = string([]rune(output)[:maxShownOutput]) + "\n… [output truncated]"
	} else if result.Truncated {
		output += "\n… [output truncated]"
	}
	if result.TimedOut {
		notes = append(notes, "[The command ran past the time limit and was stopped]")
	}
	text := output + "\n[Command exited with code " + strconv.Itoa(result.ExitCode) + "]"
	for _, note := range notes {
		text += "\n" + note
	}
	return text
}

// maxReadFileBytes bounds a file read into the conversation.
const maxReadFileBytes = 100 << 10

func (s Specialist) readFileTool(owner Owner) tool.Tool {
	return tool.Tool{
		Name:        ReadFileTool,
		Description: "Read a UTF-8 text file from the sandbox, up to 100 KB. Binary files are not read: they are checked with verify_artifact and stored with save_artifact.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"The file's absolute path in the sandbox."}},"required":["path"]}`),
		Approval:    tool.Auto,
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return "", fmt.Errorf("the arguments are not readable: %w", err)
			}
			where, err := sandboxPath(args.Path)
			if err != nil {
				return "", err
			}
			bench, err := s.bench(ctx, owner)
			if err != nil {
				return "", err
			}
			data, err := bench.ReadFile(ctx, where, maxReadFileBytes)
			switch {
			case errors.Is(err, sandbox.ErrNotFound):
				return "", fmt.Errorf("there is no file at %s", where)
			case errors.Is(err, sandbox.ErrTooLarge):
				return "", fmt.Errorf("%s is larger than %d bytes; read part of it with a command (head, sed -n)", where, maxReadFileBytes)
			case err != nil:
				return "", s.sandboxFailure(ctx, owner, "reading the file", err)
			case !utf8.Valid(data) || strings.ContainsRune(string(data), 0):
				return "", errors.New(ReadFileTool + " reads UTF-8 text only; a binary file is checked with " + VerifyTool + " and stored with " + SaveTool)
			}
			return string(data), nil
		},
	}
}
