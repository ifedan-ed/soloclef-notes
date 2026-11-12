package deliverables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// A standalone image: the specialist writes a prompt, the image model draws
// it, and the file is saved as an artifact the page's image card shows in
// the chat. There is no sandbox and no second tool: one call makes, saves
// and answers.
//
// What this does better than the tool it replaces:
//
//   - One image per call. The old tool took "n" up to 4, asked the provider
//     for that many and saved the first.
//   - The model that draws is the deployment's one image model, by the
//     proxy's name. There is no "auto" choice and no second model tried
//     after a failure.
//   - A failure tells the person which kind of failure it was (refused
//     prompt, busy, not set up). It used to show the exception's text.

// ImageTool is keyed to the page's image card (components/tool-ui/
// generate-image).
const ImageTool = "generate_image"

// FormatImage and FormatInfographic are the two formats a picture is saved
// under. The strings are the artifacts table's and the viewer registry's.
const (
	FormatImage       = "image"
	FormatInfographic = "infographic"
)

// kindImage is the kind whose finisher is ImageTool.
const kindImage = "image"

// finishers names the tool that finishes a kind which is not saved with
// save_artifact. Every other kind in the kinds table is one of
// save_artifact's formats; the package's tests hold the two to each other.
var finishers = map[string]string{kindPodcast: PodcastTool, kindImage: ImageTool, kindVideo: VideoTool}

// The bounds of an image prompt and of the title made from it.
const (
	maxImagePromptChars = 4000
	maxImageTitleChars  = 120
)

const imageInstructions = `Image: one standalone picture, shown in the chat.

Call ` + ImageTool + ` once, with:
- prompt: a detailed description of the picture, up to 4000 characters. Say the subject, the style, the colours, the composition and the mood. Describe what to draw; do not paste the source material.

One call makes one image. If the task asks for several different images, make one call for each, at most four.
Do not call save_artifact for an image: the tool saves it and shows it to the user.
Never use this for an infographic, a visual explainer or a diagram of data or steps: that is the kind "infographic".
Answer only that the image was made and what it shows. Never write a link to it.`

// ImageMade is what generate_image answers: the page's image card draws
// from every field but Message, which is the model's. The card fetches the
// picture by ArtifactID alone; whose it is, the server decides from the
// caller when the file is asked for.
type ImageMade struct {
	// ID names the image for the page's own bookkeeping.
	ID         string `json:"id"`
	ArtifactID int64  `json:"artifact_id"`
	// Alt is what the image shows, in words.
	Alt   string `json:"alt"`
	Title string `json:"title"`
	// Description is the provider's own prompt when it rewrote the one it
	// was given.
	Description string `json:"description,omitempty"`
	// Domain and Ratio are the card's caption and frame; Generated marks it
	// as made by a model.
	Domain    string `json:"domain"`
	Ratio     string `json:"ratio"`
	Generated bool   `json:"generated"`
	Prompt    string `json:"prompt"`
	Message   string `json:"message,omitempty"`
}

// ImageFailed is the card of an image that was not made.
type ImageFailed struct {
	Error  string `json:"error"`
	Prompt string `json:"prompt,omitempty"`
}

const imageMadeMessage = "The image is saved and is shown to the user in the chat. Say in a sentence what it shows; do not write a link or an id."

// refusedImageNote follows a refusal's sentence in what the model reads, and
// is taken off again before the sentence reaches the person's card.
const refusedImageNote = " The image was not made; tell the requester exactly that."

// newImagePrompt checks a prompt and returns it trimmed.
func newImagePrompt(prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	switch {
	case prompt == "":
		return "", errors.New("the image was not made: prompt is empty")
	case utf8.RuneCountInString(prompt) > maxImagePromptChars:
		return "", fmt.Errorf("the image was not made: prompt is longer than %d characters", maxImagePromptChars)
	}
	if bad := badCharacter(prompt); bad != "" {
		return "", errors.New("the image was not made: prompt contains " + bad)
	}
	return prompt, nil
}

// imageTitle is an image's title: the start of its prompt, on one line.
func imageTitle(prompt string) string {
	title := strings.Join(strings.Fields(prompt), " ")
	if runes := []rune(title); len(runes) > maxImageTitleChars {
		title = strings.TrimSpace(string(runes[:maxImageTitleChars]))
	}
	return title
}

// SaveImage stores a generated image as a new artifact. Its document is the
// prompt, which is what makes the image findable by the account's search.
func (s Saver) SaveImage(ctx context.Context, owner Owner, toolCallID, prompt, modelName string, image GeneratedImage) (Saved, error) {
	title := imageTitle(prompt)
	record := map[string]any{"prompt": prompt}
	if modelName != "" {
		record["model"] = modelName
	}
	if image.RevisedPrompt != "" {
		record["revised_prompt"] = image.RevisedPrompt
	}
	metadata, err := json.Marshal(record)
	if err != nil {
		return Saved{}, err
	}
	return s.save(ctx, NewArtifact{
		Owner: owner, ToolCallID: toolCallID, Title: title, Format: FormatImage,
		Markdown: "# " + title + "\n\nPrompt: " + prompt + "\n", Metadata: metadata,
		Filename: slug(title, FormatImage) + "." + image.Extension, MimeType: image.MimeType, Data: image.Data,
	})
}

func (s Specialist) imageTool(owner Owner) tool.Tool {
	return tool.Tool{
		Name:        ImageTool,
		Description: "Draw one image from a description and show it to the user. It answers the image that was made, or why it was not.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"What to draw: subject, style, colours, composition, mood. Up to 4000 characters."}},"required":["prompt"],"additionalProperties":false}`),
		// It draws what the user asked this chat for and saves it in their
		// own account; it sends nothing anywhere else and is undone by
		// deleting the artifact. It does not ask.
		Approval: tool.Auto,
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			var args struct {
				Prompt string `json:"prompt"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(call.Args)))
			// "n", "size", "style": a model that passes one believes it
			// took effect, so it is told that it did not.
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&args); err != nil {
				return "", fmt.Errorf("the arguments do not fit the schema: %w", err)
			}
			prompt, err := newImagePrompt(args.Prompt)
			if err != nil {
				return "", err
			}
			image, err := s.Images.Generate(ctx, prompt, "")
			if err != nil {
				return "", s.imageError(ctx, owner, err, "the image", refusedImageNote)
			}
			saved, err := s.Saver.SaveImage(ctx, owner, call.ID, prompt, s.Images.Name(), image)
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				s.Saver.Logger.ErrorContext(ctx, "an image could not be saved", "error", err.Error(), "user_id", owner.UserID)
				return "", errors.New("the image was drawn but could not be saved because of a storage error; it was not saved")
			}
			alt := prompt
			if image.RevisedPrompt != "" {
				alt = image.RevisedPrompt
			}
			out, err := json.Marshal(ImageMade{
				ID: "image-artifact-" + strconv.FormatInt(saved.ArtifactID, 10), ArtifactID: saved.ArtifactID,
				Alt: alt, Title: saved.Title, Description: image.RevisedPrompt,
				Domain: "ai-generated", Ratio: "auto", Generated: true, Prompt: prompt, Message: imageMadeMessage,
			})
			return string(out), err
		},
	}
}

// imageError is what the model reads when the image model did not deliver:
// a refusal's own sentence, or a plain one with the cause in the log.
func (s Specialist) imageError(ctx context.Context, owner Owner, err error, what, note string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var refused *ImageRefusal
	if errors.As(err, &refused) {
		if cause := errors.Unwrap(err); cause != nil {
			s.Saver.Logger.LogAttrs(ctx, slog.LevelWarn, "the image model did not deliver",
				slog.String("reason", refused.Reason), slog.String("error", cause.Error()), slog.String("user_id", owner.UserID))
		}
		return errors.New(refused.Reason + note)
	}
	s.Saver.Logger.ErrorContext(ctx, "an image could not be generated", "error", err.Error(), "user_id", owner.UserID)
	return errors.New(what + " could not be made because the image model failed." + note)
}

// imageCard is the page's image card for a finished generate_image call.
func imageCard(result event.Event) (any, bool) {
	// The card shows the prompt beside a failure; it is read from the call,
	// which is where the page has it too.
	var args struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal([]byte(result.Args), &args)
	if result.IsError {
		return ImageFailed{Error: cardSentence(result.Text, refusedImageNote), Prompt: args.Prompt}, true
	}
	var made ImageMade
	if json.Unmarshal([]byte(result.Text), &made) != nil || made.ArtifactID <= 0 {
		return nil, false
	}
	// The message is the model's; the card has no use for it.
	made.Message = ""
	return made, true
}

// cardSentence is a failed call's text as a card may show it: without the
// loop's "error: " prefix and the note meant for the model, and bounded.
func cardSentence(text, note string) string {
	reason := strings.TrimSuffix(strings.TrimPrefix(text, "error: "), note)
	if utf8.RuneCountInString(reason) > maxCardErrorChars {
		reason = string([]rune(reason)[:maxCardErrorChars])
	}
	return reason
}
