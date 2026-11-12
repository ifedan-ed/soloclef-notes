package deliverables

import "errors"

// canvas is the shape an infographic is drawn on: the size the image model
// is asked for, and the same shape in words in the prompt, because a model
// behind the proxy that maps or ignores the size still reads the prompt.
type canvas struct {
	id string
	// size is "WIDTHxHEIGHT" as the image route takes it. The three are the
	// sizes the OpenAI image models draw, which the proxy maps for the
	// others: the same shapes, never a size a model refuses.
	size string
	// words describe the shape to the model.
	words string
}

// canvases is every orientation, landscape first: the studio dialog's
// default, and the shape an infographic page has always been drawn in.
var canvases = []canvas{
	{"landscape", "1536x1024", "Canvas: landscape, 3:2, wider than it is tall. Compose the whole layout for that shape."},
	{"portrait", "1024x1536", "Canvas: portrait, 2:3, taller than it is wide. Compose the whole layout for that shape, reading top to bottom."},
	{"square", "1024x1024", "Canvas: square, 1:1. Compose the whole layout for that shape."},
}

// InfographicOrientations is every orientation an infographic may be asked
// for, the default first: what a dialog that asks for one may send.
func InfographicOrientations() []string {
	ids := make([]string, len(canvases))
	for index, shape := range canvases {
		ids[index] = shape.id
	}
	return ids
}

// canvasNamed is the canvas of an orientation. "" is no orientation asked
// (a chat's infographic, whose question card asks only for a style): the
// zero canvas, which asks the model for no size and says nothing of the
// shape, so the model draws its own. Any other unknown name is an error,
// not a guess.
func canvasNamed(id string) (canvas, error) {
	if id == "" {
		return canvas{}, nil
	}
	for _, shape := range canvases {
		if shape.id == id {
			return shape, nil
		}
	}
	return canvas{}, errors.New("\"" + id + "\" is not an infographic orientation; no infographic was made")
}
