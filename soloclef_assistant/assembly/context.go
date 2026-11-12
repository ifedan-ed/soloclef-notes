package assembly

import (
	"fmt"
	"strings"
)

// TurnContext is what one turn's prompt takes from the database: the
// account's own instructions, who is asking and what is remembered about
// them, and the notebook the thread belongs to.
//
// It is read at the start of every turn and held nowhere. The prompt it
// becomes is sent with each model call and is not part of the stored
// conversation, so a memory edited, a name changed or a source unticked is
// in the very next message's prompt, and a long thread carries no old copy
// of any of it. (The chat this replaces appended the memory block to the
// stored conversation on every turn: a thread of forty turns carried forty
// copies and paid for them on every call.)
type TurnContext struct {
	// Instructions is the account's standing instructions for every chat.
	Instructions string
	// Citations is whether the account's answers cite sources.
	Citations bool

	// UserName is the asker's display name, or "".
	UserName string
	// UserMemory is the account's memory document, in Markdown.
	UserMemory string

	// Notebook is the notebook a notebook chat belongs to, or nil for a
	// plain chat.
	Notebook *Notebook
}

// Notebook is a notebook chat's confinement and its "Configure chat"
// choices.
type Notebook struct {
	ID    int64
	Title string
	// DocumentIDs is the hard limit of the chat's searches: the notebook's
	// enabled document sources and the documents in its enabled folder
	// sources. Empty means the notebook has nothing enabled, and then
	// nothing may be read: it never means "no limit".
	DocumentIDs []int64
	// SourceTitles names the enabled sources for the prompt, documents
	// first, in the order they were added; a folder's name ends in "/".
	SourceTitles []string

	// Goal, Instructions and Length are the notebook's chat settings, as
	// stored: goal "default", "learning_guide" or "custom"; length
	// "default", "longer" or "shorter".
	Goal         string
	Instructions string
	Length       string
}

// The sizes of what a turn's context may add to the prompt. Each is a bound
// on text a person wrote, so that no one document can fill a model's
// context by itself; none is a deployment's choice.
const (
	// MaxMemoryChars is the most of a memory document shown: the most one
	// may hold. A row longer than that was written some other way than
	// through the checks, and update_memory refuses to replace it, because
	// the model has not seen all of it. It is the limit the API tier's
	// memory routes hold a document to (markdownmemory.HardLimit); a test
	// there keeps the two equal.
	MaxMemoryChars = 25_000
	// maxListedSources is how many of a notebook's sources are named. The
	// search still reads all of them.
	maxListedSources = 40
	// maxSourceTitleChars and maxNotebookInstructionChars bound one title
	// and the notebook's own instructions.
	maxSourceTitleChars         = 200
	maxNotebookInstructionChars = 2_000
)

// inert makes a person's text safe to put inside one of the prompt's
// blocks: angle brackets are replaced, so the text cannot close the block it
// is in and open one of its own.
func inert(text string) string {
	return strings.NewReplacer("<", "‹", ">", "›").Replace(text)
}

// clipped is text cut to at most max characters.
func clipped(text string, max int) string {
	if runes := []rune(text); len(runes) > max {
		return string(runes[:max])
	}
	return text
}

// oneLine is text on one line, cut to max characters.
func oneLine(text string, max int) string {
	return clipped(strings.Join(strings.Fields(text), " "), max)
}

// personBlock is who is asking and what is remembered about them: the
// asker's first name and their memory. It is the document the turn's
// update_memory tool writes (Assembly.Build), and the block ends with that
// tool's instructions, so the model is never told to write a document it
// was not shown.
func personBlock(in TurnContext, writing Remembering) string {
	var b strings.Builder
	if fields := strings.Fields(in.UserName); len(fields) > 0 {
		// The first name only: it is what a person is called, and the rest
		// of a display name is not something to address them by.
		b.WriteString("<user_name>" + inert(clipped(fields[0], 60)) + "</user_name>\n\n")
	}
	b.WriteString(memoryBlock("user_memory", "What the user has asked you to remember about them.", in.UserMemory, writing))
	if writing != nil {
		b.WriteString(writing.Protocol())
	}
	return b.String()
}

// memoryBlock shows one memory document. An empty one is shown too, as
// empty: the model can write it, so it must know there is nothing to merge
// with rather than wonder whether a document exists that it was not shown.
// How full the document is goes in the heading line because the model is
// asked to stay within the limit.
func memoryBlock(tag, what, document string, writing Remembering) string {
	document = strings.TrimSpace(document)
	head := "<" + tag + ">\n" + what + " Use it where it bears on the question; do not recite it."
	if writing != nil {
		// How full it is matters only to a turn that can write it.
		head += " (" + writing.Usage(document) + ")"
	}
	head += "\n"
	if document == "" {
		return head + "Nothing is saved yet.\n</" + tag + ">\n\n"
	}
	return head + inert(clipped(document, MaxMemoryChars)) + "\n</" + tag + ">\n\n"
}

// notebookBlock tells the model it is inside a notebook: what it may read,
// and how the notebook's owner asked it to answer. The confinement itself
// is not here. It is in the search (documents.Scope.Limited) and in which
// tools the turn has; this block only explains to the model what it will
// find, so that it says "that is not in this notebook" instead of "I found
// nothing".
func notebookBlock(notebook *Notebook, pickedTool bool) string {
	if notebook == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("<notebook_context>\n")
	if notebook.Title != "" {
		b.WriteString("This conversation belongs to the notebook \"" + inert(oneLine(notebook.Title, maxSourceTitleChars)) + "\".\n")
	} else {
		b.WriteString("This conversation belongs to a notebook.\n")
	}
	if len(notebook.DocumentIDs) == 0 && len(notebook.SourceTitles) == 0 {
		b.WriteString("The notebook has no enabled sources yet, so there is nothing to search: the search reaches the notebook's sources and nothing else. Unless you already said so in this conversation, say so briefly at the start of your answer, then offer next steps: adding files or websites as sources, or ticking sources already in the notebook.\n")
	} else {
		b.WriteString("Answer from its enabled sources. search_knowledge_base reaches only these; nothing else the user has (other notebooks' sources, generated outputs) is available. Ground answers in them and cite them with [n]. If the sources do not answer the question, say so and suggest adding sources; do not offer to search the web.\n")
		if pickedTool {
			b.WriteString("Anything the tool the user picked for this message returns is from outside the notebook: say so when you use it.\n")
		}
		b.WriteString("Enabled sources:\n")
		titles := notebook.SourceTitles
		hidden := 0
		if len(titles) > maxListedSources {
			hidden = len(titles) - maxListedSources
			titles = titles[:maxListedSources]
		}
		for _, title := range titles {
			b.WriteString("- " + inert(oneLine(title, maxSourceTitleChars)) + "\n")
		}
		if hidden > 0 {
			b.WriteString(fmt.Sprintf("- … and %d more\n", hidden))
		}
	}
	switch notebook.Goal {
	case "custom":
		if custom := oneLine(notebook.Instructions, maxNotebookInstructionChars); custom != "" {
			b.WriteString("The notebook's owner set these instructions for how to respond; follow them where they do not conflict with the rules above: " + inert(custom) + "\n")
		}
	case "learning_guide":
		b.WriteString("Act as a learning guide: help the user understand the material rather than only handing over answers. Break ideas into steps, check understanding with a short question now and then, and suggest what to explore next.\n")
	}
	switch notebook.Length {
	case "longer":
		b.WriteString("Prefer longer, more thorough answers with more detail and examples.\n")
	case "shorter":
		b.WriteString("Prefer short, direct answers; expand only when asked.\n")
	}
	b.WriteString("</notebook_context>\n\n")
	return b.String()
}
