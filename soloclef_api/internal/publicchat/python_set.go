package publicchat

import "math/big"

// intSet is a CPython `set` of integers, kept for one thing only: the order
// it is iterated in.
//
// That order reaches a public answer. The page's legacy upcast collects
// artifact ids into a set and hands the set to SQLAlchemy's `in_()`, which
// decides the SQL type of the *whole* list from its first value
// (upcast.go:askable). An id of `true` is in that set as True, so whether the
// request is a 200 or a 500 turns on whether True is the first thing the set
// yields -- and that is a property of CPython's hash table, not of the data's
// order: {True, 900} yields True first, {True, 8} yields 8 first.
//
// So this is Objects/setobject.c, as far as adding and iterating go: open
// addressing over a power-of-two table, nine linear probes before each
// perturbed jump, growth to four times the entries once the table is three
// fifths full. It is held to CPython's own answers for 800 sets
// (testdata/python_sets.json, recorded from the interpreter).
// Deletion, and with it dummy entries, is not here: the route never removes
// from the set.
type intSet struct {
	slots []*setEntry
	used  int
}

type setEntry struct {
	value  *big.Int
	isBool bool
	hash   int64
}

const (
	setMinimumSize  = 8 // PySet_MINSIZE
	setLinearProbes = 9 // LINEAR_PROBES
	setPerturbShift = 5 // PERTURB_SHIFT
)

// hashModulus is 2**61 - 1, the prime CPython reduces an integer by to hash
// it on a 64-bit build (_PyHASH_MODULUS).
var hashModulus = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 61), big.NewInt(1))

// pythonHash is hash() of an int: the value itself reduced modulo 2**61 - 1,
// keeping its sign, with -1 replaced by -2 because -1 is CPython's error
// return. hash(True) is 1 and hash(False) is 0, the hashes of the integers
// they equal.
func pythonHash(value *big.Int) int64 {
	magnitude := new(big.Int).Abs(value)
	reduced := magnitude.Mod(magnitude, hashModulus).Int64()
	if value.Sign() < 0 {
		reduced = -reduced
	}
	if reduced == -1 {
		return -2
	}
	return reduced
}

// Add is `s.add(value)`. A value equal to one already there changes nothing,
// and "equal" is Python's: adding 1 to a set holding True leaves True, and
// adding True to a set holding 1 leaves 1.
func (s *intSet) Add(value *big.Int, isBool bool) {
	if s.slots == nil {
		s.slots = make([]*setEntry, setMinimumSize)
	}
	entry := &setEntry{value: value, isBool: isBool, hash: pythonHash(value)}
	mask := uint64(len(s.slots) - 1)
	perturb := uint64(entry.hash)
	index := perturb & mask
	for {
		// Up to nine neighbours are tried before jumping, but only when the
		// run does not cross the end of the table.
		probes := uint64(0)
		if index+setLinearProbes <= mask {
			probes = setLinearProbes
		}
		for offset := uint64(0); offset <= probes; offset++ {
			held := s.slots[index+offset]
			if held == nil {
				s.slots[index+offset] = entry
				s.used++
				// Grown once three fifths full: fill*5 >= mask*3.
				if uint64(s.used)*5 >= mask*3 {
					s.resize()
				}
				return
			}
			if held.hash == entry.hash && held.value.Cmp(value) == 0 {
				return
			}
		}
		perturb >>= setPerturbShift
		index = (index*5 + 1 + perturb) & mask
	}
}

// resize is set_table_resize: a table of the smallest power of two above
// four times the entries, refilled from the old table in *slot* order -- not
// in the order the values were added, which is why the order a set yields
// can change when it grows.
func (s *intSet) resize() {
	wanted := s.used * 4
	if s.used > 50000 {
		wanted = s.used * 2
	}
	size := setMinimumSize
	for size <= wanted {
		size <<= 1
	}
	old := s.slots
	s.slots = make([]*setEntry, size)
	mask := uint64(size - 1)
	for _, entry := range old {
		if entry == nil {
			continue
		}
		// set_insert_clean: the same walk, with nothing to compare against.
		perturb := uint64(entry.hash)
		index := perturb & mask
	placing:
		for {
			probes := uint64(0)
			if index+setLinearProbes <= mask {
				probes = setLinearProbes
			}
			for offset := uint64(0); offset <= probes; offset++ {
				if s.slots[index+offset] == nil {
					s.slots[index+offset] = entry
					break placing
				}
			}
			perturb >>= setPerturbShift
			index = (index*5 + 1 + perturb) & mask
		}
	}
}

// Ordered is `list(s)`: the entries in table order.
func (s *intSet) Ordered() []*setEntry {
	ordered := make([]*setEntry, 0, s.used)
	for _, entry := range s.slots {
		if entry != nil {
			ordered = append(ordered, entry)
		}
	}
	return ordered
}
