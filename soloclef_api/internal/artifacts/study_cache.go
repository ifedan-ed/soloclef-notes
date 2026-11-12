package artifacts

import (
	"container/list"
	"context"
	"io"
	"slices"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// StudyFiles reads flashcard decks and quizzes out of the file store for the
// seven routes that need one, and remembers what it read.
//
// # Why remembering is safe
//
// A study file's storage key carries a fresh uuid every time a file is
// written (app/artifacts/keys.py), and a key is never written twice: when the
// agent revises a deck it writes a new file under a new key and points the
// row at it. So what a key's file says -- how many cards, which answers are
// right -- can never change while the key exists. A remembered answer cannot
// go stale; there is nothing to invalidate and no time-to-live to tune.
//
// # Why it is worth it
//
// The manifest is the most-called route of the package: the web app refetches
// it every time the window regains focus. Each flashcard flip and each quiz
// answer is a write that needs the same file. Without this, every one of
// those opens the file and tokenises it -- about 70 microseconds and 1,500
// allocations for an ordinary deck, 11 milliseconds and 2 MB for the largest
// one the format allows (BenchmarkStudyFile*). Remembered, it is a map lookup
// under a mutex: about 75 nanoseconds and no allocation, whatever the deck's
// size (BenchmarkStudyFilesRemembered).
//
// # What is not remembered
//
// Only a file that was read successfully. A file that is not a study file is
// rare and is reported to the person anyway; a store that failed may work on
// the next request, and caching its failure would turn one bad moment into a
// lasting one.
//
// The memory is bounded: at most studyFilesRemembered entries, the least
// recently used dropped first. An entry is a key and at most thirty small
// integers, so the whole cache is well under a megabyte.
type StudyFiles struct {
	// stores is every store this process reads; a file's row says which.
	stores   *filestore.Stores
	capacity int

	mutex sync.Mutex
	// byKey finds an entry; recency orders them, most recently used at the
	// front. The two always hold the same entries.
	byKey   map[studyFileKey]*list.Element
	recency *list.List
}

// studyFilesRemembered is how many study files one process remembers: far
// more decks and quizzes than are open at any one moment on a server this
// size, and still a trivial amount of memory.
const studyFilesRemembered = 4096

// studyFileKey names what was read: which file, and as which kind of study
// file. The backend is part of the key because a key names an object only
// within one store: the same key in the bucket and on the disk are two
// objects, and what was read from one must never answer for the other. The
// kind is part of the key because the two readers keep different
// facts about a file, and a file read as one must never answer as the other.
type studyFileKey struct {
	backend, key string
	quiz         bool
}

// studyFacts is what a study route needs from a file: the number of cards,
// or the right option of each question (whose length is the number of
// questions).
type studyFacts struct {
	key     studyFileKey
	cards   int
	correct []int
}

// NewStudyFiles reads study files out of `stores`, each from the backend its
// row records. One is built per process
// and shared by the manifest and the six study writes, so that a deck read
// for one is remembered for all.
func NewStudyFiles(stores *filestore.Stores) *StudyFiles {
	return newStudyFiles(stores, studyFilesRemembered)
}

func newStudyFiles(stores *filestore.Stores, capacity int) *StudyFiles {
	return &StudyFiles{
		stores:   stores,
		capacity: capacity,
		byKey:    make(map[studyFileKey]*list.Element, capacity),
		recency:  list.New(),
	}
}

// Deck counts the cards of an artifact's deck.
//
// The error is an `unreadable` when the file is at fault (there is none, or
// it is not a deck) and anything else when the store is, exactly as
// readDeck's.
func (s *StudyFiles) Deck(ctx context.Context, file *StoredFile) (int, error) {
	facts, err := s.facts(ctx, file, false, func(source io.Reader) (studyFacts, error) {
		cards, err := readDeck(source)
		return studyFacts{cards: cards}, err
	})
	return facts.cards, err
}

// Quiz reads the right option of each question of an artifact's quiz. The
// slice is the caller's own: changing it changes nothing remembered.
func (s *StudyFiles) Quiz(ctx context.Context, file *StoredFile) ([]int, error) {
	facts, err := s.facts(ctx, file, true, func(source io.Reader) (studyFacts, error) {
		correct, err := readQuiz(source)
		return studyFacts{correct: correct}, err
	})
	if err != nil {
		return nil, err
	}
	return slices.Clone(facts.correct), nil
}

// errNoStudyFile is an artifact of a study format with no primary file.
var errNoStudyFile = unreadable{"it has no file"}

// facts answers from memory when it can, and otherwise reads the file --
// outside the mutex, so that one slow read never holds up the requests for
// other decks -- and remembers what it found.
//
// Two requests that miss on the same file at the same moment both read it.
// That costs one redundant read, once; preventing it would mean making the
// second wait on the first, and a wait is worse than a read this cheap.
func (s *StudyFiles) facts(
	ctx context.Context,
	file *StoredFile,
	quiz bool,
	read func(io.Reader) (studyFacts, error),
) (studyFacts, error) {
	if file == nil {
		return studyFacts{}, errNoStudyFile
	}
	// Memory first: it is keyed by backend and key together, so an answer
	// found here was read from the very store this row names.
	name := studyFileKey{backend: file.Backend, key: file.Key, quiz: quiz}
	if facts, remembered := s.recall(name); remembered {
		return facts, nil
	}

	// The row says which store holds the bytes, and that is the one read. If
	// this process was given no settings for it, the bytes are somewhere it
	// cannot reach: looking under the same key in a store it does have could
	// find a different object. The error is the store set's own
	// (filestore.ErrBackendNotRead) and not an `unreadable`, so the routes
	// report it as the server's fault and not as a bad deck.
	store, err := s.stores.Named(file.Backend)
	if err != nil {
		return studyFacts{}, err
	}
	stream, err := store.Open(ctx, file.Key)
	if err != nil {
		return studyFacts{}, err
	}
	defer stream.Close()
	facts, err := read(stream)
	if err != nil {
		return studyFacts{}, err
	}
	facts.key = name
	s.remember(facts)
	return facts, nil
}

func (s *StudyFiles) recall(name studyFileKey) (studyFacts, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	element, found := s.byKey[name]
	if !found {
		return studyFacts{}, false
	}
	s.recency.MoveToFront(element)
	return element.Value.(studyFacts), true
}

func (s *StudyFiles) remember(facts studyFacts) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if element, found := s.byKey[facts.key]; found {
		// Another request read the same file meanwhile. Its facts are the
		// same facts, since the file cannot have changed.
		s.recency.MoveToFront(element)
		return
	}
	s.byKey[facts.key] = s.recency.PushFront(facts)
	for s.recency.Len() > s.capacity {
		oldest := s.recency.Back()
		s.recency.Remove(oldest)
		delete(s.byKey, oldest.Value.(studyFacts).key)
	}
}
