package recursive

// splitEveryNCharacters is the last level: with no delimiter left to cut at,
// chonkie encodes the text with its tokenizer and slices the token sequence
// into runs of chunkSize. The configured tokenizer is chonkie's `character`
// one, whose tokens are the text's characters and whose decode is exact, so
// the cut lands every chunkSize characters -- characters, not bytes.
func splitEveryNCharacters(text string, n int) []string {
	var out []string
	count, start := 0, 0
	for i := range text {
		if count == n {
			out = append(out, text[start:i])
			start, count = i, 0
		}
		count++
	}
	if start < len(text) {
		out = append(out, text[start:])
	}
	return out
}

func characterCount(text string) int {
	count := 0
	for range text {
		count++
	}
	return count
}
