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

// Or merges other's set bits into b, growing b if other is longer — the
// "child's bitfield OR'd with its parent's" mechanic role inheritance
// resolution needs (auth-internals.md §10 "Role inheritance").
func (b *PermissionBitfield) Or(other PermissionBitfield) {
	if len(other) > len(*b) {
		grown := make(PermissionBitfield, len(other))
		copy(grown, *b)
		*b = grown
	}

	for word, bits := range other {
		(*b)[word] |= bits
	}
}

// And restricts b to only the bits also set in other — auth-internals.md §7
// "Scope restriction": an API key's scopes narrow, never expand, its
// associated user's permission set.
func (b *PermissionBitfield) And(other PermissionBitfield) {
	for word := range *b {
		if word >= len(other) {
			(*b)[word] = 0
		} else {
			(*b)[word] &= other[word]
		}
	}
}
