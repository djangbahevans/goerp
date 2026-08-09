package permission

const bitsPerWord = 64

// PermissionBitfield is a []uint64 bitfield indexed by a PermissionRegistry
// index, giving O(1) Has via a single word AND.
type PermissionBitfield []uint64

func (b PermissionBitfield) Has(idx int) bool {
	word := idx / bitsPerWord
	if word >= len(b) {
		return false
	}

	return b[word]&(1<<(idx%bitsPerWord)) != 0
}

// Set marks idx, growing the bitfield if idx falls past its current length.
func (b *PermissionBitfield) Set(idx int) {
	word := idx / bitsPerWord
	if word >= len(*b) {
		grown := make(PermissionBitfield, word+1)
		copy(grown, *b)
		*b = grown
	}

	(*b)[word] |= 1 << (idx % bitsPerWord)
}
