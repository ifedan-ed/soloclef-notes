// Package deliverables is the specialist that makes artifacts: the things a
// chat produces that outlive the conversation and open in their own viewer.
//
// It is a nested run of the same loop the chat runs on (agent.Delegate). The
// main agent hands it a task in words; it loads the instructions for the
// kind of artifact asked for, builds it, and saves it. It is a specialist
// rather than a set of tools on the main agent because the procedure and its
// instructions are long, and should not sit in every conversation that never
// makes an artifact.
//
// # What is built
//
// End to end:
//
//   - Flashcards and quizzes: the specialist's model writes the deck or the
//     quiz as the arguments of save_artifact; it is checked against the
//     whole schema the web viewer enforces; the JSON file is stored; the
//     document, artifact and file rows are written in one transaction; and
//     the result drives the page's save_artifact card. The artifact is then
//     listed, opened and studied through internal/artifacts' routes.
//   - Mind maps: the model writes the hierarchy as Markdown; it is checked
//     and stored in one canonical spelling as the artifact's document, with
//     no file, which is what the viewer draws from (mindmap.go).
//   - Podcasts: generate_podcast opens a podcast at its brief gate with a
//     brief proposed; the page's podcast card takes it from there
//     (podcast.go). Nothing is drafted or queued until the person approves.
//   - Reports (PDF), Word documents, slide decks, workbooks and interactive
//     pages: built by code the model runs in the thread's sandbox, checked
//     structurally here, rendered there by LibreOffice and poppler, looked
//     at by a model that can see, and saved only as the bytes that passed
//     (documents.go, verify.go, structure.go).
//
// The kinds are one table (kinds, below). The instructions tool, the
// specialist's own prompt, the delegate's description and the text the main
// agent's prompt is given (ArtifactsPrompt) are all written from it, so no
// prompt can offer a kind this package cannot make.
//
// # What the other kinds need
//
// Every other kind is produced by something this tier does not have yet,
// and each is refused by name rather than attempted badly:
//
//   - Slides with narration and video: a queued render job (Remotion in the
//     sandbox, speech for the narration) that the worker tier runs; this
//     side needs the enqueue tool and its card.
//   - Image and infographic: the image model through the model proxy, and
//     for an infographic the style question, which is a tool.AskUser pause.
//   - A plain Markdown document: the path a mind map takes (a document and
//     no file) under the format "markdown", and a decision about what such
//     a document may contain.
//
// # What this does better than the specialist it replaces
//
//   - A deck or a quiz is checked completely before anything is stored, and
//     every problem is reported at once. It used to be written to a sandbox
//     file, verified by a second tool, and saved by a third, with a signed
//     receipt passed between them to prove the three had seen the same
//     bytes.
//   - The file and its rows are one unit: a failed save leaves no rows and
//     no orphaned file.
//   - Who the artifact belongs to (account, thread, person) is fixed by
//     the server for the turn. The model cannot name another account.
//   - A quiz question that does not say which option is right is refused;
//     so is one without a rationale per option, which the viewer shows and
//     the old validator only asked for in its prompt.
//   - A mind map needs no sandbox and no browser, is stored in one spelling
//     whatever indentation the model used, and a label that is itself
//     Markdown structure ("1. Steps", "> note") is refused instead of being
//     drawn as a different tree.
//   - A podcast is opened only for a member who may create podcasts, with
//     the same statement the podcast routes use.
package deliverables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// The names the model and the page know.
const (
	// DelegateName is the tool the main agent hands an artifact task to.
	DelegateName = "deliverables"
	// SaveTool is keyed to the page's save_artifact card.
	SaveTool = "save_artifact"
	// InstructionsTool loads one kind's instructions.
	InstructionsTool = "load_artifact_instructions"
)

// kind is one kind of artifact this specialist makes.
type kind struct {
	// name is what load_artifact_instructions is called with, and for a
	// saved kind the artifact's format.
	name string
	// label is the kind in a sentence: "flashcards", "a quiz".
	label string
	// means is how to tell a request for this kind from one for something
	// near it, for the specialist's model.
	means string
	// instructions is what load_artifact_instructions answers. It is loaded
	// on demand so that a run pays only for the kind it was asked for.
	instructions string
}

// kindPodcast is the one kind that is not a saved format.
const kindPodcast = "podcast"

// kinds is every kind that can be made, in the order prompts list them.
// This table is the only place a kind is declared: adding one here without
// its tool, or a tool without its row, fails the package's tests.
var kinds = []kind{
	{FormatFlashcards, "flashcards", "cards for recall practice: a deck, study cards, revision cards", flashcardInstructions},
	{FormatQuiz, "a quiz", "a scored multiple-choice test; not a survey, a poll, a form or a personality test", quizInstructions},
	{FormatMindMap, "a mind map", "a topic laid out as a hierarchy of concepts; not a flowchart, a sequence diagram or a free-form drawing", mindMapInstructions},
	{kindPodcast, "a podcast", "an audio episode discussing the material", podcastInstructions},
	{FormatInfographic, "an infographic", "the material as one designed image with headings, labels and figures: a visual explainer, a data story, a process poster; not a plain picture", infographicInstructions},
	{kindImage, "an image", "one standalone picture or illustration; not an infographic and not a chart of data", imageInstructions},
	// The five built by code in the sandbox (documents.go). Their
	// instructions are the skills in the sandbox image, read from it when
	// asked for, so they always describe the libraries that image has.
	{FormatPDF, "a PDF report", "a typeset document to read or print: a report, a summary, a handout, a letter, a CV", ""},
	{FormatDOCX, "a Word document", "an editable document the user will open in Word; only when they ask for Word or a .docx", ""},
	{FormatPPTX, "a slide deck", "presentation slides as a PowerPoint file; not a narrated video", ""},
	{FormatXLSX, "a spreadsheet", "tabular data, calculations or a tracker as an Excel workbook", ""},
	{FormatHTML, "an interactive page", "a small self-contained interactive tool: a calculator, a simulator, a widget, a dashboard", ""},
	{kindVideo, "a video", "narrated slides rendered as a video file: an explainer video, a narrated presentation; not a slide deck", videoInstructions},
}

// kindNames is the kinds' names, for a sentence.
func kindNames() string {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = k.name
	}
	return joined(names)
}

// kindLabels is the kinds as a phrase: "flashcards, a quiz, a mind map or a
// podcast".
func kindLabels() string {
	labels := make([]string, len(kinds))
	for i, k := range kinds {
		labels[i] = k.label
	}
	return joined(labels)
}

// joined is words as an English list ending in "or".
func joined(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}

// nameSeparators removes what is not part of a kind's name.
var nameSeparators = strings.NewReplacer(" ", "", "-", "", "_", "")

// kindNamed finds a kind by what a model wrote for it. Case, spaces,
// hyphens and underscores are not part of a name: "Mind map" and "mind_map"
// are the kind "mindmap". Nothing else is guessed.
func kindNamed(written string) (kind, string, bool) {
	name := nameSeparators.Replace(strings.ToLower(strings.TrimSpace(written)))
	for _, k := range kinds {
		if k.name == name {
			return k, name, true
		}
	}
	return kind{}, name, false
}

// The three texts written from the table. They are built once, when the
// package loads.
var (
	delegateDescription = `Create something the user can open, keep and study: ` + kindLabels() + `. It makes nothing else. Give it a complete task: which kind, what it is about, how many items if the user said, and ALL the source material to make it from (the relevant passages or facts, copied in). It sees only the task text, not this conversation and not the user's documents. It answers with what it made.`

	systemPrompt = `You make artifacts for the user of a study and research application. You are given a task by another assistant; the task text is everything you know. You do not talk to the user.

Procedure, always:
1. Decide the kind of artifact from the task. The kinds you can make:
` + kindList() + `2. Call load_artifact_instructions for that kind and follow what it returns exactly.
3. Build the artifact from the material in the task. Do not invent facts the material does not support; if the task gives a topic and no material, use well-established knowledge of that topic and say that you did.
4. Finish with the one tool the instructions name, called once. If it reports problems, fix every one and call it again. If it fails twice, stop and report the failure.

Answer in two or three plain sentences: what you made (kind, title, how many items), or exactly why you could not. Never write a download link or an id. If the task asks for a kind you cannot make, say so in one sentence and make nothing; never make a different kind in its place.`
)

// kindList is the kinds as the lines of the specialist's prompt.
func kindList() string {
	var b strings.Builder
	for _, k := range kinds {
		b.WriteString("   - " + k.name + ": " + k.means + ".\n")
	}
	return b.String()
}

// ArtifactsPrompt is the body of the main agent's <artifacts> block: which
// kinds exist and how to hand a task over. The main prompt takes it from
// here, and from nowhere else, so that it never offers a kind the
// specialist would refuse. It has no tags and no trailing line break.
func ArtifactsPrompt() string { return artifactsPrompt }

var artifactsPrompt = `The ` + DelegateName + ` tool makes things the user can open and keep: ` + kindLabels() + `. When the user asks for one of these, hand the task to it. It makes nothing else: for anything that is none of these, say plainly that it cannot be made, and do not hand the task over.
It cannot see this conversation or the user's documents: first gather the material (search the documents), then give it a complete task with the kind, the subject, how many items if the user said, and that material copied in.
Afterwards tell the user in a sentence what was made; the page shows the artifact itself, so never write a link to it. For a podcast say only that it is set up: its card in the chat shows the proposed language, voices and length for the user to confirm, and tracks everything after that, so do not describe its status. For a video say only that it is being generated: its card shows the progress and then the video, which takes several minutes, and the user can keep chatting. For an infographic the user is first asked in the chat to pick a visual style; if they close that question, nothing is made.`

const flashcardInstructions = `Flashcards.

Pass the deck to save_artifact as: format "flashcards", a title, and cards.

Each card is {"front_text": "...", "back_text": "...", "hint_text": "..."}.
- Make 5 to 100 cards. Use the number the task asks for; otherwise 30 to 50 when the material supports it, fewer when it does not. Do not pad.
- One distinct thing to recall per card. The front is a question or prompt; the back is the answer, complete enough to check oneself against. No two cards with the same front.
- Limits: title up to 200 characters on one line; front up to 4000; back up to 12000; hint up to 2000.
- Leave hint_text out when there is no useful hint. Never pass null and never add other fields.
- Text is plain text. The only formatting allowed is LaTeX for mathematics, inline \( ... \) or displayed \[ ... \], with balanced braces and never one inside another. No Markdown, no HTML.
- Cover the material evenly, most important ideas first.`

const quizInstructions = `Quiz: scored multiple choice, one right answer per question.

Pass the quiz to save_artifact as: format "quiz", a title, and questions.

Each question is {"question_text": "...", "options": ["...", "...", "...", "..."], "correct_option_index": 1, "explanation_text": "...", "option_rationales": ["...", "...", "...", "..."]}.
- Make 5 to 30 questions. Use the number the task asks for; otherwise exactly 10. If the task asks for a number outside 5 to 30, make nothing and say what the range is. If the material cannot support five sound questions, make nothing and say so. Do not pad, paraphrase or reverse a question to reach a number.
- Exactly four distinct options, exactly one of them objectively right. correct_option_index is its place counted from 0, so 0, 1, 2 or 3. Spread the right answer across the four places over the quiz.
- Three plausible distractors. Never "All of the above" or "None of the above"; no trick wording, and no clue in the grammar or the length of an option.
- explanation_text teaches, in a few sentences, why the right answer is right.
- option_rationales is required: four short entries in the same order as options. The right option's says why it is right; each wrong option's says specifically why it is wrong (the misconception behind it), from the material. One or two sentences each, never just "This is incorrect."
- Limits: title up to 200 characters on one line; question and each option up to 4000; explanation up to 12000; each rationale up to 2000.
- Text is plain text. The only formatting allowed is LaTeX for mathematics, inline \( ... \) or displayed \[ ... \], with balanced braces and never one inside another. No Markdown, no HTML, no links.
- No two questions with the same text. Never pass null and never add other fields.`

const mindMapInstructions = `Mind map: a topic as a hierarchy.

Pass the map to save_artifact as: format "mindmap", a title, and markdown.

markdown is exactly one level-one heading, the root, then a nested bullet list under it, two spaces per level:

# Photosynthesis

- Light reactions
  - Photosystem II
  - Photosystem I
- Calvin cycle
  - Carbon fixation

- Give the root 2 to 6 balanced branches where the subject supports them.
- A label is a few specific words, never a sentence or a paragraph.
- Limits, counting the root: at most 60 nodes, at most 6 levels deep, at most 120 characters per label. If the subject needs more, map its main structure and say what you left out.
- Never skip a level: an item is at most one level deeper than the item above it.
- A label is words only: no links, images, HTML, code fences or tables, and it does not begin with a number and a full stop, "#", ">" or a bullet. Emphasis and inline code are allowed.
- One heading only. Do not write anything before the root or after the list.`

const podcastInstructions = `Podcast: an audio episode made from the material.

Call ` + PodcastTool + ` once, with:
- podcast_title: a short title for the episode, one line.
- source_content: ALL the material the episode should cover, copied in full from the task. It is the only thing the episode is written from; do not summarise it into a few lines and do not pass a topic alone.
- user_prompt: only if the task says what the episode should concentrate on or how it should sound; a sentence or two. Otherwise leave it out.

Do not call save_artifact for a podcast. The tool does not make the audio: it sets the podcast up and proposes its language, voices and length, which the user confirms on a card in the chat; the episode is written and voiced after that. So answer only that the podcast is set up and waiting for the user to confirm its brief. Do not say it is being generated, how long it will take, or what its status is.`

// unavailable is the kinds a task may name that cannot be made, and what
// this specialist says about each. They are listed so the model is told
// what is missing in the user's own words for it, and never tries a kind by
// squeezing it into another.
var unavailable = map[string]string{
	"markdown": "Plain documents cannot be made in this version yet.",
}

// Limits bound one artifact task. Both are required.
type Limits struct {
	// MaxSteps is how many steps the specialist may take for one task.
	MaxSteps int
	// Timeout is how long one stretch of its work may take.
	Timeout time.Duration
}

// Specialist builds the delegate for a turn.
type Specialist struct {
	Model model.Model
	Store checkpoint.Store
	Saver Saver
	// Podcasts opens a podcast at its brief gate. Required: podcast is one
	// of the kinds every prompt offers, and a deployment with no speech
	// service still answers through it, with the sentence saying so.
	Podcasts PodcastOpener
	// Images draws an image and an infographic; Reader reads an infographic
	// back to check its text. Both are required. A deployment with no such
	// model says so with NoImageModel and NoImageReader, and the person is
	// told in a sentence.
	Images ImageModel
	Reader ImageReader
	// Videos queues a video's render job. Required.
	Videos VideoQueuer
	// Workshop is where a report, a deck, a workbook or an interactive
	// page is built, checked and read back from: the thread's sandbox
	// (documents.go). Required. A deployment with no sandbox says so with
	// NoSandbox, and the person is told in a sentence.
	Workshop Workshop
	// Verified is the record of which files passed (verify.go): required
	// wherever there is a sandbox, and unused where there is not.
	Verified *Verifications
	Limits   Limits
}

// Check reports what is missing. Called once at start-up.
func (s Specialist) Check() error {
	switch {
	case s.Model == nil:
		return errors.New("deliverables: Model is not set")
	case s.Store == nil:
		return errors.New("deliverables: Store is not set")
	case s.Podcasts == nil:
		return errors.New("deliverables: Podcasts is not set")
	case s.Images == nil:
		return errors.New("deliverables: Images is not set")
	case s.Reader == nil:
		return errors.New("deliverables: Reader is not set")
	case s.Videos == nil:
		return errors.New("deliverables: Videos is not set")
	case s.Workshop == nil:
		return errors.New("deliverables: Workshop is not set")
	case s.Limits.MaxSteps < 1:
		return fmt.Errorf("deliverables: Limits.MaxSteps must be at least 1, got %d", s.Limits.MaxSteps)
	case s.Limits.Timeout <= 0:
		return fmt.Errorf("deliverables: Limits.Timeout must be positive, got %s", s.Limits.Timeout)
	}
	if err := s.Podcasts.Check(); err != nil {
		return err
	}
	if err := s.Images.Check(); err != nil {
		return err
	}
	if err := s.Reader.Check(); err != nil {
		return err
	}
	if err := s.Videos.Check(); err != nil {
		return err
	}
	if err := s.Workshop.Check(); err != nil {
		return err
	}
	if _, none := s.Workshop.(NoSandbox); !none && s.Verified == nil {
		// With a sandbox, documents are built, and nothing built is saved
		// without the record of its verification.
		return errors.New("deliverables: Verified is not set, and there is a sandbox to build documents in")
	}
	return s.Saver.Check()
}

// Delegate returns the specialist as a tool of the main agent, for one turn.
// It is built per turn because the owner is: building it is a few map
// inserts (agent.New), so nothing is kept between turns.
func (s Specialist) Delegate(owner Owner) (agent.Delegate, error) {
	nested, err := agent.New(agent.Config{
		Model:  s.Model,
		System: systemPrompt,
		Tools: []tool.Tool{s.instructionsTool(owner), s.saveTool(owner), s.podcastTool(owner), s.imageTool(owner), s.videoTool(owner),
			s.executeTool(owner), s.readFileTool(owner), s.verifyTool(owner)},
		// One at a time: the procedure is a sequence, and two saves side
		// by side would be two artifacts.
		MaxSteps: s.Limits.MaxSteps, MaxParallelTools: 1,
		Store: s.Store,
	})
	if err != nil {
		return agent.Delegate{}, err
	}
	return agent.Delegate{Name: DelegateName, Description: delegateDescription, Agent: nested, Timeout: s.Limits.Timeout}, nil
}

func (s Specialist) instructionsTool(owner Owner) tool.Tool {
	return tool.Tool{
		Name:        InstructionsTool,
		Description: "Load the instructions for one kind of artifact. Call it before making that kind.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"artifact_type":{"type":"string","description":"The kind: ` + kindNames() + `."}},"required":["artifact_type"]}`),
		Approval:    tool.Auto,
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			var args struct {
				Kind string `json:"artifact_type"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return "", fmt.Errorf("the arguments are not readable: %w", err)
			}
			found, name, known := kindNamed(args.Kind)
			if _, built := documentFormats[name]; known && built {
				return s.documentInstructions(ctx, owner, name)
			}
			if known {
				return found.instructions, nil
			}
			if sentence, named := unavailable[name]; named {
				// An error result: the model must not go on to build it.
				return "", errors.New(sentence + " Tell the requester so and make nothing.")
			}
			return "", fmt.Errorf("%q is not a kind of artifact. The kinds that can be made are %s.", args.Kind, kindNames())
		},
	}
}

// saveParameters is save_artifact's schema: the format, the title, and one
// field per format for the content. Which content field goes with which
// format is checked when the tool runs, because a schema that said so
// (oneOf over three shapes) is one several providers refuse.
const saveParameters = `{"type":"object","properties":{` +
	`"format":{"type":"string","enum":["flashcards","quiz","mindmap","infographic","pdf","docx","pptx","xlsx","html"]},` +
	`"title":{"type":"string","description":"The artifact's title, one line, up to 200 characters."},` +
	`"cards":{"type":"array","description":"For flashcards only.","items":{"type":"object","properties":{"front_text":{"type":"string"},"back_text":{"type":"string"},"hint_text":{"type":"string"}},"required":["front_text","back_text"],"additionalProperties":false}},` +
	`"questions":{"type":"array","description":"For a quiz only.","items":{"type":"object","properties":{"question_text":{"type":"string"},"options":{"type":"array","items":{"type":"string"}},"correct_option_index":{"type":"integer","description":"The place of the right option, counted from 0."},"explanation_text":{"type":"string"},"option_rationales":{"type":"array","items":{"type":"string"}}},"required":["question_text","options","correct_option_index","explanation_text","option_rationales"],"additionalProperties":false}},` +
	`"markdown":{"type":"string","description":"For a mind map: one # heading, then a nested bullet list. For an infographic: the plan, a # title, ## sections and short bullets."}` +
	`,"path":{"type":"string","description":"For pdf, docx, pptx, xlsx and html only: the file in the sandbox, as it was passed to verify_artifact."}` +
	`,"markdown_representation":{"type":"string","description":"For pdf, docx, pptx, xlsx and html only: the artifact's substantive content as Markdown, which is what search and screen readers get. The text itself: no shell runs on these arguments, so $(cat file) or a file path is refused."}` +
	`},"required":["format","title"],"additionalProperties":false}`

// saveArguments is what save_artifact is called with. The three content
// fields can each be told apart from "not passed": a nil slice or pointer is
// a field the model left out.
type saveArguments struct {
	Format    string     `json:"format"`
	Title     string     `json:"title"`
	Cards     []Card     `json:"cards"`
	Questions []Question `json:"questions"`
	Markdown  *string    `json:"markdown"`
	// Path and Representation are for the formats built in the sandbox:
	// the file there, and its content as Markdown.
	Path           *string `json:"path"`
	Representation *string `json:"markdown_representation"`
}

// contentField is the one content field a format is saved from.
var contentField = map[string]string{FormatFlashcards: "cards", FormatQuiz: "questions", FormatMindMap: "markdown", FormatInfographic: "markdown"}

// misplaced names the content fields passed that do not belong to the
// format. A quiz passed with cards as well is a model that has mixed two
// sets of instructions, and saving the half that fits would be saving
// something other than what it believes it saved.
func (a saveArguments) misplaced() []string {
	var extra []string
	for _, field := range []struct {
		name   string
		passed bool
	}{{"cards", a.Cards != nil}, {"questions", a.Questions != nil}, {"markdown", a.Markdown != nil}} {
		if field.passed && field.name != contentField[a.Format] {
			extra = append(extra, field.name)
		}
	}
	return extra
}

func (s Specialist) saveTool(owner Owner) tool.Tool {
	return tool.Tool{
		Name:        SaveTool,
		Description: "Save the finished artifact so the user can open it. Pass format, title, and the one content field of that format: cards for flashcards, questions for a quiz, markdown for a mind map. For a file built in the sandbox (pdf, docx, pptx, xlsx, html) pass format, title, path and markdown_representation instead, after verify_artifact has passed that path. It answers what was saved, or every problem to fix.",
		Parameters:  json.RawMessage(saveParameters),
		// Saving creates a study aid in the user's own account at their
		// request; it sends nothing anywhere and is undone by deleting the
		// artifact. It does not ask.
		Approval: tool.Auto,
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			var args saveArguments
			decoder := json.NewDecoder(strings.NewReader(string(call.Args)))
			// A field the schema does not have is a model inventing one
			// ("tags", "difficulty"); say so instead of dropping it and
			// saving something other than what the model believes it saved.
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&args); err != nil {
				return "", fmt.Errorf("the arguments do not fit the schema: %w", err)
			}
			if _, built := documentFormats[args.Format]; built {
				// A file built in the sandbox: saved from the bytes that
				// verify_artifact passed, not from these arguments.
				return s.saveDocument(ctx, owner, call.ID, args)
			}
			if args.Path != nil || args.Representation != nil {
				return "", fmt.Errorf("format %q is not saved from a sandbox file; remove path and markdown_representation", args.Format)
			}
			field, saved := contentField[args.Format]
			if !saved {
				switch found, name, _ := kindNamed(args.Format); {
				case finishers[name] != "":
					// A kind with a tool of its own: a podcast, an image,
					// a video.
					return "", errors.New(found.label + " is not saved with " + SaveTool + "; call " + finishers[name])
				case unavailable[name] != "":
					return "", errors.New(unavailable[name] + " Tell the requester so and make nothing.")
				}
				return "", fmt.Errorf("format %q cannot be saved; the formats that can are flashcards, quiz and mindmap", args.Format)
			}
			if extra := args.misplaced(); len(extra) > 0 {
				return "", fmt.Errorf("format %q is saved from %s alone; remove %s", args.Format, field, strings.Join(extra, " and "))
			}

			if args.Format == FormatInfographic {
				// An infographic is drawn, checked and saved by its own
				// procedure, which pauses to ask the person for a style
				// (infographic.go). Its answer and its errors are already
				// worded for the model.
				return s.infographic(ctx, owner, call, args.Title, args.Markdown)
			}
			var result Saved
			var err error
			switch args.Format {
			case FormatFlashcards:
				var deck Deck
				if deck, err = NewDeck(args.Title, args.Cards); err != nil {
					return "", err
				}
				result, err = s.Saver.SaveFlashcards(ctx, owner, call.ID, deck)
			case FormatQuiz:
				var quiz Quiz
				if quiz, err = NewQuiz(args.Title, args.Questions); err != nil {
					return "", err
				}
				result, err = s.Saver.SaveQuiz(ctx, owner, call.ID, quiz)
			case FormatMindMap:
				if args.Markdown == nil {
					return "", errors.New("the mind map was not saved: markdown is missing")
				}
				var mindMap MindMap
				if mindMap, err = NewMindMap(args.Title, *args.Markdown); err != nil {
					return "", err
				}
				result, err = s.Saver.SaveMindMap(ctx, owner, call.ID, mindMap)
			}
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				s.Saver.Logger.ErrorContext(ctx, "an artifact could not be saved", "error", err.Error(), "user_id", owner.UserID, "format", args.Format)
				// The cause can name tables and paths; the model gets a
				// sentence it can pass on.
				return "", errors.New("the artifact could not be saved because of a storage error; it was not saved")
			}
			out, err := json.Marshal(result)
			return string(out), err
		},
	}
}

// PodcastOpened is what generate_podcast answers: what the model is told,
// and, less its message, what the page's podcast card draws from.
type PodcastOpened struct {
	// Status is the podcast's status as created, "awaiting_brief".
	Status    string `json:"status"`
	PodcastID int64  `json:"podcast_id"`
	Title     string `json:"title"`
	// Message is for the model only: what happens next, and what not to say.
	Message string `json:"message,omitempty"`
}

// statusAwaitingBrief is the status a podcast is created in
// (queries.sql, CreatePodcast).
const statusAwaitingBrief = "awaiting_brief"

// refusedPodcastNote follows a refusal's sentence in what the model reads.
// It is for the model alone, and ToolCard takes it off again before the
// sentence reaches the person.
const refusedPodcastNote = " The podcast was not created; tell the requester exactly that."

const podcastOpenedMessage = "The podcast is set up. Its card in the chat shows the proposed language, voices and length; the user confirms them there, and the episode is written and voiced automatically after that. The card tracks progress live, so do not state the podcast's status or say that it is being generated."

func (s Specialist) podcastTool(owner Owner) tool.Tool {
	return tool.Tool{
		Name:        PodcastTool,
		Description: "Set up a podcast from the material for the user to review. It proposes the language, voices and length, which the user confirms on a card in the chat; the episode is made after that. It answers the podcast that was set up, or every problem to fix.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"podcast_title":{"type":"string","description":"The episode's title, one line, up to 500 characters."},"source_content":{"type":"string","description":"All the material the episode is made from, copied in full."},"user_prompt":{"type":"string","description":"Optional: what the episode should concentrate on, up to 2000 characters."}},"required":["podcast_title","source_content"],"additionalProperties":false}`),
		// Opening a podcast writes one row in the user's own account and
		// asks no model. What costs something, drafting and voicing, starts
		// only when the person approves the brief on the card, so that
		// approval is the one that matters and this does not ask first.
		Approval: tool.Auto,
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			var args struct {
				Title  string `json:"podcast_title"`
				Source string `json:"source_content"`
				Focus  string `json:"user_prompt"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(call.Args)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&args); err != nil {
				return "", fmt.Errorf("the arguments do not fit the schema: %w", err)
			}
			request, err := newPodcastRequest(args.Title, args.Source, args.Focus)
			if err != nil {
				return "", err
			}
			podcastID, err := s.Podcasts.Open(ctx, owner, request)
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				var refused *PodcastRefusal
				if errors.As(err, &refused) {
					return "", errors.New(refused.Reason + refusedPodcastNote)
				}
				s.Saver.Logger.ErrorContext(ctx, "a podcast could not be opened from chat", "error", err.Error(), "user_id", owner.UserID)
				return "", errors.New("the podcast could not be created because of a storage error; it was not created")
			}
			out, err := json.Marshal(PodcastOpened{Status: statusAwaitingBrief, PodcastID: podcastID, Title: request.Title, Message: podcastOpenedMessage})
			return string(out), err
		},
	}
}
