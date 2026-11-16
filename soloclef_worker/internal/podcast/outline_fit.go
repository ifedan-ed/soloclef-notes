package podcast

// fitOutline holds an outline's beats to the episode's word ceiling. The
// outline prompt asks for beats that add up to about the target, but a model
// planning a "Short" episode often sizes its beats to more, and each beat is
// then drafted to its own size: the episode runs past the length the person
// chose. Beats over the ceiling are scaled down together, each keeping its
// share, so the plan's shape is the model's and only its total is held. A
// plan within the ceiling is left exactly as it is.
func fitOutline(plan outline, ceiling int) outline {
	total := 0
	for _, beat := range plan.Segments {
		total += beat.TargetWords
	}
	if ceiling <= 0 || total <= ceiling {
		return plan
	}
	fitted := outline{Segments: make([]segment, len(plan.Segments))}
	for index, beat := range plan.Segments {
		beat.TargetWords = max(1, beat.TargetWords*ceiling/total)
		fitted.Segments[index] = beat
	}
	return fitted
}
