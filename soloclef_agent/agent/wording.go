package agent

import (
	"fmt"
	"strings"
)

// The sentences in this file are written to the model, as the result of a
// tool call that did not run normally. They are in one place because they are
// prompt text: their wording changes what the model does next, and a reviewer
// should be able to read all of it at once.

// interruptedText answers a call whose turn ended before the call finished.
// It says the outcome is unknown, because it is: the tool may have completed
// its work an instant before the process stopped. Telling the model "it did
// not run" would invite a second run of something that already happened.
const interruptedText = "error: this tool call was interrupted before it finished. Whether it took effect is unknown. Check before trying it again."

func rejectionText(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "The user declined this tool call. Do not try it again unless they ask."
	}
	return "The user declined this tool call and said: " + reason
}

// unknownToolText lists the real tools, so a model that misspelled a name can
// correct itself on the next step instead of guessing again.
func unknownToolText(name string, names []string) string {
	if len(names) == 0 {
		return fmt.Sprintf("error: there is no tool named %q. No tools are available.", name)
	}
	return fmt.Sprintf("error: there is no tool named %q. The tools are: %s.", name, strings.Join(names, ", "))
}

const badArgsText = "error: the arguments were not a JSON object. Call the tool again with arguments that match its schema."

func toolErrorText(err error) string {
	return "error: " + err.Error()
}

// continueText asks the model to go on with an answer the output cap cut
// short. It forbids the two things models otherwise do: repeat the last
// sentence, and open with "Continuing from where I left off".
const continueText = "Your previous response was cut off before it finished. Continue exactly where you left off. Do not repeat any text you already wrote and do not add any preamble."
