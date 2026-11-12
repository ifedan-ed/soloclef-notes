// Package chonkie is the token counting and token splitting the chunker needs,
// done by calling chonkie's own Rust core and HuggingFace's tokenizers rather
// than by reimplementing either.
//
// Why not pure Go
// ---------------
// This module's rule is that a cut landing one character from chonkie's is a
// corpus that no longer matches its index (../AGENTS.md). Sizing a chunk in
// BGE-M3 tokens means reproducing XLM-RoBERTa Unigram tokenization, including
// its precompiled character map -- and an implementation of that which is
// right on ninety-nine documents and wrong on the hundredth is worse than
// none, because the hundredth is silently misindexed and nothing says so.
//
// So this calls the same code the Python side runs: the `chunk` crate at the
// exact version the installed chonkie-core wraps, and the `tokenizers`
// library, reading the same tokenizer.json the backend image bakes.
// Agreement is a property of the build rather than something to test and hope
// for. What it costs is the module's "no dependencies, pure Go" property,
// which was a real one: a cgo build needs a C toolchain and the static library
// built beside it (see rust/README.md). That trade was Daniel's call on
// 2026-11-10; the alternative on offer was a hand-written Unigram
// implementation whose correctness could only ever be sampled.
//
// Measured before it was written: on a 246,503-character document from the
// live corpus, the token count (61,659), all 121 decoded 512-token pieces and
// all 137 merge_splits outputs were byte-for-byte identical to Python's.
package chonkie

/*
#cgo CFLAGS: -I${SRCDIR}/rust
#cgo LDFLAGS: -L${SRCDIR}/rust/target/release -lsoloclef_chonkie -lm
#include <stdlib.h>
#include <stddef.h>
#include <sys/types.h>

void  *sc_tokenizer_open(const char *path, char **err);
void   sc_tokenizer_close(void *handle);
void   sc_string_free(char *s);
void   sc_buffer_free(unsigned char *ptr, size_t len);
ssize_t sc_count(void *handle, const unsigned char *text, size_t len, char **err);
unsigned char *sc_split_tokens(void *handle, const unsigned char *text, size_t len,
                               size_t size, size_t *out_len, char **err);
unsigned char *sc_merge_splits(const unsigned char *splits, size_t splits_len,
                               const size_t *counts, size_t counts_len, size_t size,
                               size_t *out_len, size_t **out_counts,
                               size_t *out_counts_len, char **err);
size_t *sc_split_offsets(void *handle, const unsigned char *text, size_t len,
                         size_t size, size_t *out_count, char **err);
size_t *sc_split_at_patterns(const unsigned char *text, size_t len,
                             const unsigned char *patterns, size_t patterns_len,
                             int include_delim, size_t min_chars,
                             size_t *out_count, char **err);
size_t *sc_split_at_delimiters(const unsigned char *text, size_t len,
                               const unsigned char *delimiters, size_t delimiters_len,
                               int include_delim, size_t min_chars,
                               size_t *out_count, char **err);
void   sc_offsets_free(size_t *ptr, size_t count);
const char *sc_core_version(void);
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

// CoreVersion is the chonkie-core version this binary was built against.
//
// Compare it with `pip show chonkie-core` in the backend image: the two must
// be the same, because a different core may merge in a different place.
func CoreVersion() string {
	return C.GoString(C.sc_core_version())
}

// Tokenizer is a loaded vocabulary. It is safe for concurrent use: the
// underlying tokenizers::Tokenizer is immutable once loaded and every call
// here only reads it.
type Tokenizer struct {
	handle unsafe.Pointer
	// closed guards against a second Close freeing the handle twice, and
	// against a call racing one.
	mu     sync.RWMutex
	closed bool
}

// Open loads tokenizer.json from path.
//
// path is the file itself, not the directory holding it: the backend bakes it
// at /app/tokenizer/tokenizer.json, named by EMBEDDING_TOKENIZER.
func Open(path string) (*Tokenizer, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var cErr *C.char
	handle := C.sc_tokenizer_open(cPath, &cErr)
	if handle == nil {
		return nil, takeErr(cErr, "opening the tokenizer failed without saying why")
	}
	t := &Tokenizer{handle: unsafe.Pointer(handle)}
	// A tokenizer that goes out of scope unclosed would leak the vocabulary,
	// which for BGE-M3 is about 17 MB. Close is still the right thing to call.
	runtime.SetFinalizer(t, func(t *Tokenizer) { _ = t.Close() })
	return t, nil
}

// Close frees the vocabulary. It is safe to call more than once.
func (t *Tokenizer) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	C.sc_tokenizer_close(t.handle)
	t.handle = nil
	runtime.SetFinalizer(t, nil)
	return nil
}

var errClosed = errors.New("chonkie: the tokenizer is closed")

// Count is how many tokens text holds, in this tokenizer's vocabulary.
//
// Special tokens are not added, because chonkie counts the text's own tokens:
// adding a <s>/</s> pair would make every count two too high and so every
// chunk two tokens smaller than it should be.
func (t *Tokenizer) Count(text string) (int, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.closed {
		return 0, errClosed
	}
	ptr, length := bytesOf(text)
	var cErr *C.char
	n := C.sc_count(t.handle, ptr, length, &cErr)
	if n < 0 {
		return 0, takeErr(cErr, fmt.Sprintf("counting tokens failed (code %d)", int(n)))
	}
	return int(n), nil
}

// SplitTokens is chonkie's last recursion level as chonkie writes it: encode
// text, cut the token sequence into runs of size, and decode each run back to
// text.
//
// Prefer SplitTokensVerbatim. This one is kept to reproduce chonkie exactly,
// for comparing against the Python side, and it loses text while doing it: a
// decode of BGE-M3 ids is not the bytes that produced them. SentencePiece has
// no newline token and carries a leading space as a mark on the piece, so
// every newline comes back as a space and a space on a piece boundary
// disappears. A piece from here is therefore not necessarily a substring of
// text -- see TestSplitTokensDoesNotRoundTrip.
func (t *Tokenizer) SplitTokens(text string, size int) ([]string, error) {
	if size < 1 {
		return nil, fmt.Errorf("chonkie: chunk size must be at least 1, got %d", size)
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.closed {
		return nil, errClosed
	}
	ptr, length := bytesOf(text)
	var outLen C.size_t
	var cErr *C.char
	buf := C.sc_split_tokens(t.handle, ptr, length, C.size_t(size), &outLen, &cErr)
	if buf == nil {
		return nil, takeErr(cErr, "splitting by tokens failed without saying why")
	}
	defer C.sc_buffer_free(buf, outLen)
	return unpack(buf, outLen)
}

// SplitOffsets is where to cut text so each piece holds at most size tokens,
// as byte offsets into text itself.
//
// It is SplitTokens without the decode, and it is the one to use. The decode
// loses text: see SplitTokensVerbatim.
//
// The result is the cut points, not the pieces. It starts at 0 and ends at
// len(text), so it holds one more entry than there are pieces, and an empty
// text gives a single entry and so no pieces.
func (t *Tokenizer) SplitOffsets(text string, size int) ([]int, error) {
	if size < 1 {
		return nil, fmt.Errorf("chonkie: chunk size must be at least 1, got %d", size)
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.closed {
		return nil, errClosed
	}
	ptr, length := bytesOf(text)
	var outCount C.size_t
	var cErr *C.char
	buf := C.sc_split_offsets(t.handle, ptr, length, C.size_t(size), &outCount, &cErr)
	if buf == nil {
		return nil, takeErr(cErr, "finding token offsets failed without saying why")
	}
	defer C.sc_offsets_free(buf, outCount)

	// A Go slice over Rust's memory, read only, and only until that free.
	raw := unsafe.Slice((*C.size_t)(unsafe.Pointer(buf)), int(outCount))
	cuts := make([]int, len(raw))
	for i, at := range raw {
		cuts[i] = int(at)
		// The Rust side promises these are ordered and inside the text. Check
		// it rather than trust it: a bad offset here would slice a string in
		// the wrong place, and a chunk cut in the wrong place is exactly the
		// failure this whole package exists to prevent.
		if cuts[i] < 0 || cuts[i] > len(text) {
			return nil, fmt.Errorf(
				"chonkie: token offset %d is %d, outside a %d-byte text",
				i, cuts[i], len(text))
		}
		if i > 0 && cuts[i] <= cuts[i-1] {
			return nil, fmt.Errorf(
				"chonkie: token offset %d (%d) does not come after offset %d (%d)",
				i, cuts[i], i-1, cuts[i-1])
		}
	}
	return cuts, nil
}

// SplitTokensVerbatim cuts text into runs of at most size tokens, as slices of
// text itself.
//
// This is what the last recursion level should do, and what SplitTokens does
// not. Every piece is a substring of text, and joining the pieces gives text
// back byte for byte -- so a chunk can always be found in the document it came
// from again, which is what citation line numbers depend on.
//
// The pieces share text's backing array; nothing is copied.
func (t *Tokenizer) SplitTokensVerbatim(text string, size int) ([]string, error) {
	cuts, err := t.SplitOffsets(text, size)
	if err != nil {
		return nil, err
	}
	if len(cuts) < 2 {
		return nil, nil
	}
	pieces := make([]string, 0, len(cuts)-1)
	for i := 1; i < len(cuts); i++ {
		pieces = append(pieces, text[cuts[i-1]:cuts[i]])
	}
	return pieces, nil
}

// MergeSplits is chonkie's merge, from chonkie's own core: the pieces a
// delimiter level produced, joined back up into runs of at most size tokens.
//
// counts must hold one token count per split, in the same order -- the core
// does not tokenize, so the caller supplies them. Use Tokenizer.CountAll.
//
// It returns the merged pieces AND one token count per merged piece, and the
// second return value matters as much as the first. The core's count for a
// merged piece is the sum of the counts of the pieces it joined, which is not
// what re-counting the merged text would give: with a SentencePiece
// vocabulary "a"+"b" does not tokenize like "a" and "b" apart. chonkie
// decides whether a merged piece has to be cut again at the next level by
// comparing this sum against the chunk size, so a caller that recounted would
// take a different branch and cut in a different place.
//
// This is a function rather than a method on Tokenizer because the core does
// not tokenize here: no vocabulary is consulted, only the counts handed in.
func MergeSplits(splits []string, counts []int, size int) ([]string, []int, error) {
	if size < 1 {
		return nil, nil, fmt.Errorf("chonkie: chunk size must be at least 1, got %d", size)
	}
	if len(splits) != len(counts) {
		return nil, nil, fmt.Errorf(
			"chonkie: %d splits but %d counts", len(splits), len(counts))
	}
	if len(splits) == 0 {
		return nil, nil, nil
	}
	packed := pack(splits)
	cCounts := make([]C.size_t, len(counts))
	for i, c := range counts {
		if c < 0 {
			return nil, nil, fmt.Errorf("chonkie: split %d has a negative token count", i)
		}
		cCounts[i] = C.size_t(c)
	}
	var outLen, outCountsLen C.size_t
	var outCounts *C.size_t
	var cErr *C.char
	buf := C.sc_merge_splits(
		(*C.uchar)(unsafe.Pointer(&packed[0])), C.size_t(len(packed)),
		(*C.size_t)(unsafe.Pointer(&cCounts[0])), C.size_t(len(cCounts)),
		C.size_t(size), &outLen, &outCounts, &outCountsLen, &cErr,
	)
	// packed and cCounts are read by Rust only for the duration of the call,
	// and this keeps Go from collecting them before it returns.
	runtime.KeepAlive(packed)
	runtime.KeepAlive(cCounts)
	if buf == nil {
		return nil, nil, takeErr(cErr, "merging splits failed without saying why")
	}
	defer C.sc_buffer_free(buf, outLen)
	defer C.sc_offsets_free(outCounts, outCountsLen)

	merged, err := unpack(buf, outLen)
	if err != nil {
		return nil, nil, err
	}
	mergedCounts := readSizes(outCounts, outCountsLen)
	if len(merged) != len(mergedCounts) {
		return nil, nil, fmt.Errorf(
			"chonkie: the core returned %d merged splits but %d counts for them",
			len(merged), len(mergedCounts))
	}
	return merged, mergedCounts, nil
}

// IncludeDelim says which side of a cut the delimiter that caused it goes to.
// It is chonkie's `include_delim`, which every level of its default rules sets
// to "prev".
type IncludeDelim int

const (
	// IncludePrev leaves the delimiter at the end of the piece it closed, so
	// the pieces still concatenate back into the text.
	IncludePrev IncludeDelim = 0
	// IncludeNext puts the delimiter at the start of the piece after it.
	IncludeNext IncludeDelim = 1
	// IncludeNone drops the delimiter, so the pieces no longer reconstruct
	// the text.
	IncludeNone IncludeDelim = 2
)

func (i IncludeDelim) valid() bool {
	return i == IncludePrev || i == IncludeNext || i == IncludeNone
}

// Span is a half-open byte range [Start, End) into the text that produced it.
type Span struct {
	Start, End int
}

// SplitAtPatterns cuts text at every occurrence of any of patterns, as
// chonkie's `split_pattern_offsets` does.
//
// This is the level chonkie reaches for whenever a level's delimiter list
// holds a delimiter longer than one character. Matching is leftmost-first, so
// where two patterns start at the same byte the one listed FIRST wins -- the
// order of the list is part of where the text is cut, not presentation.
//
// minChars is chonkie's min_characters_per_chunk. A cut is only taken where
// the piece it closes and the piece that follows both reach it, so the short
// pieces around a one-word line are absorbed into their neighbours instead of
// standing alone. Its name says characters and the core measures it in BYTES;
// that is the core's behaviour and it is reproduced, not corrected.
//
// The spans are byte ranges into text, in order and non-overlapping.
func SplitAtPatterns(text string, patterns []string, include IncludeDelim, minChars int) ([]Span, error) {
	if !include.valid() {
		return nil, fmt.Errorf("chonkie: include_delim %d is not one of IncludePrev, IncludeNext, IncludeNone", int(include))
	}
	if len(patterns) == 0 {
		return nil, errors.New("chonkie: splitting at patterns needs at least one pattern")
	}
	for i, pattern := range patterns {
		if pattern == "" {
			return nil, fmt.Errorf("chonkie: pattern %d is empty", i)
		}
	}
	if minChars < 0 {
		return nil, fmt.Errorf("chonkie: min_characters_per_chunk must not be negative, got %d", minChars)
	}
	// The core answers "no segments" for an empty text; saying so here keeps
	// a null pointer out of the packed-pattern argument below.
	if text == "" {
		return nil, nil
	}
	packed := pack(patterns)
	ptr, length := bytesOf(text)
	var outCount C.size_t
	var cErr *C.char
	buf := C.sc_split_at_patterns(
		ptr, length,
		(*C.uchar)(unsafe.Pointer(&packed[0])), C.size_t(len(packed)),
		C.int(include), C.size_t(minChars), &outCount, &cErr,
	)
	runtime.KeepAlive(packed)
	if buf == nil {
		return nil, takeErr(cErr, "splitting at patterns failed without saying why")
	}
	defer C.sc_offsets_free(buf, outCount)
	return spansOf(readSizes(buf, outCount), text)
}

// SplitAtDelimiters cuts text at every byte of delimiters, as chonkie's
// `split_offsets` does. It is the whitespace level, where chonkie passes a
// single space.
//
// delimiters is a byte SET: each of its bytes cuts on its own. chonkie builds
// it by joining a level's delimiters and encoding the result, which is why a
// level holding anything multi-byte has to go to SplitAtPatterns -- passed
// here, an em dash would become three unrelated byte delimiters that each cut
// inside a character.
//
// minChars is as in SplitAtPatterns.
func SplitAtDelimiters(text string, delimiters string, include IncludeDelim, minChars int) ([]Span, error) {
	if !include.valid() {
		return nil, fmt.Errorf("chonkie: include_delim %d is not one of IncludePrev, IncludeNext, IncludeNone", int(include))
	}
	if delimiters == "" {
		return nil, errors.New("chonkie: splitting at delimiters needs at least one delimiter byte")
	}
	if minChars < 0 {
		return nil, fmt.Errorf("chonkie: min_characters_per_chunk must not be negative, got %d", minChars)
	}
	if text == "" {
		return nil, nil
	}
	textPtr, textLen := bytesOf(text)
	delimPtr, delimLen := bytesOf(delimiters)
	var outCount C.size_t
	var cErr *C.char
	buf := C.sc_split_at_delimiters(
		textPtr, textLen, delimPtr, delimLen,
		C.int(include), C.size_t(minChars), &outCount, &cErr,
	)
	if buf == nil {
		return nil, takeErr(cErr, "splitting at delimiters failed without saying why")
	}
	defer C.sc_offsets_free(buf, outCount)
	return spansOf(readSizes(buf, outCount), text)
}

// readSizes copies a size_t array Rust owns into Go memory.
//
// The copy is the point: the slice below is a view of Rust's allocation, and
// it stops being valid the moment the caller's deferred free runs.
func readSizes(buf *C.size_t, count C.size_t) []int {
	if buf == nil || count == 0 {
		return nil
	}
	raw := unsafe.Slice(buf, int(count))
	out := make([]int, len(raw))
	for i, value := range raw {
		out[i] = int(value)
	}
	return out
}

// spansOf turns the flattened [start, end, start, end, ...] the Rust side
// returns into spans, checking each one against text.
//
// Checked rather than trusted: a span that ran past the end or backwards
// would slice a string in the wrong place, and a chunk cut in the wrong place
// is the failure this whole package exists to prevent.
func spansOf(flat []int, text string) ([]Span, error) {
	if len(flat)%2 != 0 {
		return nil, fmt.Errorf("chonkie: got %d offsets, which is not a whole number of spans", len(flat))
	}
	spans := make([]Span, 0, len(flat)/2)
	for i := 0; i < len(flat); i += 2 {
		span := Span{Start: flat[i], End: flat[i+1]}
		if span.Start < 0 || span.End > len(text) || span.Start > span.End {
			return nil, fmt.Errorf(
				"chonkie: span %d is %d..%d, not inside a %d-byte text",
				i/2, span.Start, span.End, len(text))
		}
		if i > 0 && span.Start < spans[len(spans)-1].End {
			return nil, fmt.Errorf(
				"chonkie: span %d starts at %d, before span %d ended at %d",
				i/2, span.Start, i/2-1, spans[len(spans)-1].End)
		}
		spans = append(spans, span)
	}
	return spans, nil
}

// CountAll is one token count per split, in order.
func (t *Tokenizer) CountAll(splits []string) ([]int, error) {
	counts := make([]int, len(splits))
	for i, split := range splits {
		n, err := t.Count(split)
		if err != nil {
			return nil, fmt.Errorf("chonkie: counting split %d: %w", i, err)
		}
		counts[i] = n
	}
	return counts, nil
}

// bytesOf is text as a pointer and a length Rust can read.
//
// The empty string has no first byte to address, so it goes across as a null
// pointer with length zero, which the Rust side reads as "".
func bytesOf(text string) (*C.uchar, C.size_t) {
	if len(text) == 0 {
		return nil, 0
	}
	return (*C.uchar)(unsafe.Pointer(unsafe.StringData(text))), C.size_t(len(text))
}

// takeErr turns the Rust side's owned C string into a Go error and frees it.
func takeErr(cErr *C.char, fallback string) error {
	if cErr == nil {
		return errors.New("chonkie: " + fallback)
	}
	message := C.GoString(cErr)
	C.sc_string_free(cErr)
	return errors.New("chonkie: " + message)
}

// The packed layout both sides use: little-endian uint32 count, then that many
// uint32 byte lengths, then the bytes end to end. One allocation per call.
const (
	headerWord = 4
)

func pack(items []string) []byte {
	total := 0
	for _, item := range items {
		total += len(item)
	}
	out := make([]byte, 0, headerWord+headerWord*len(items)+total)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(items)))
	for _, item := range items {
		out = binary.LittleEndian.AppendUint32(out, uint32(len(item)))
	}
	for _, item := range items {
		out = append(out, item...)
	}
	return out
}

func unpack(buf *C.uchar, length C.size_t) ([]string, error) {
	if length == 0 {
		return nil, nil
	}
	// A Go slice over Rust's memory: read only, and only until the deferred
	// sc_buffer_free in the caller. The strings built below are copies.
	bytes := unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(length))
	if len(bytes) < headerWord {
		return nil, errors.New("chonkie: returned buffer is shorter than its own header")
	}
	count := int(binary.LittleEndian.Uint32(bytes))
	header := headerWord + headerWord*count
	if len(bytes) < header {
		return nil, fmt.Errorf("chonkie: returned buffer holds no lengths for %d items", count)
	}
	items := make([]string, 0, count)
	at := header
	for i := 0; i < count; i++ {
		size := int(binary.LittleEndian.Uint32(bytes[headerWord+headerWord*i:]))
		end := at + size
		if end > len(bytes) || end < at {
			return nil, fmt.Errorf("chonkie: item %d runs past the end of the buffer", i)
		}
		items = append(items, string(bytes[at:end]))
		at = end
	}
	return items, nil
}
