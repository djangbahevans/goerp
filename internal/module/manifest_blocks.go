package module

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// generatedBlock is the generated value of one top-level manifest key. A nil
// value means the key is not declared and is left out of the manifest.
type generatedBlock struct {
	key   string
	value jsontext.Value
}

// manifestMember locates one top-level member of a manifest's JSON object.
type manifestMember struct {
	key        string
	nameStart  int
	valueStart int
	valueEnd   int
}

// rewriteManifest replaces, adds or removes the top-level keys named by
// blocks in raw and returns the new manifest with the keys it changed. Every
// member of raw that is not one of blocks' keys, and every block whose value
// is already current, keeps its bytes exactly; with no block changed the
// result is raw itself.
func rewriteManifest(raw []byte, blocks []generatedBlock) ([]byte, []string, error) {
	members, braceEnd, err := scanManifestMembers(raw)
	if err != nil {
		return nil, nil, err
	}

	lead := ""
	if len(members) > 0 {
		lead = string(raw[braceEnd:members[0].nameStart])
	}
	style := newBlockStyle(lead)

	replacements := make(map[string][]byte)
	removed := make(map[string]bool)
	var appended []generatedBlock
	var changed []string

	for _, b := range blocks {
		i := slices.IndexFunc(members, func(m manifestMember) bool { return m.key == b.key })
		switch {
		case i < 0 && b.value == nil:
		case i < 0:
			appended = append(appended, b)
			changed = append(changed, b.key)
		case b.value == nil:
			removed[b.key] = true
			changed = append(changed, b.key)
		default:
			current := raw[members[i].valueStart:members[i].valueEnd]
			if sameJSON(current, b.value) {
				continue
			}
			formatted, err := style.format(b.value)
			if err != nil {
				return nil, nil, fmt.Errorf("format %s: %w", b.key, err)
			}
			replacements[b.key] = formatted
			changed = append(changed, b.key)
		}
	}
	if len(changed) == 0 {
		return raw, nil, nil
	}

	var kept []int
	for i, m := range members {
		if !removed[m.key] {
			kept = append(kept, i)
		}
	}
	if len(kept) == 0 {
		return nil, nil, errors.New("manifest has no keys other than generated ones")
	}

	var out bytes.Buffer
	out.Write(raw[:members[0].nameStart])
	for n, i := range kept {
		if n > 0 {
			out.WriteString(style.gap(raw, members, kept[n-1]))
		}
		m := members[i]
		out.Write(raw[m.nameStart:m.valueStart])
		if v, ok := replacements[m.key]; ok {
			out.Write(v)
		} else {
			out.Write(raw[m.valueStart:m.valueEnd])
		}
	}
	for _, b := range appended {
		formatted, err := style.format(b.value)
		if err != nil {
			return nil, nil, fmt.Errorf("format %s: %w", b.key, err)
		}
		key, err := json.Marshal(b.key)
		if err != nil {
			return nil, nil, fmt.Errorf("encode key %q: %w", b.key, err)
		}
		out.WriteString(style.defaultGap)
		out.Write(key)
		out.WriteString(style.colon)
		out.Write(formatted)
	}
	out.Write(raw[members[len(members)-1].valueEnd:])

	slices.Sort(changed)
	return out.Bytes(), changed, nil
}

// scanManifestMembers returns the byte spans of raw's top-level members and
// the offset just past the opening brace.
func scanManifestMembers(raw []byte) ([]manifestMember, int, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		return nil, 0, errors.New("manifest is not a JSON object")
	}
	braceEnd := int(dec.InputOffset())

	var members []manifestMember
	for dec.PeekKind() != '}' {
		name, err := dec.ReadValue()
		if err != nil {
			return nil, 0, fmt.Errorf("read manifest key: %w", err)
		}
		nameEnd := int(dec.InputOffset())

		var key string
		if err := json.Unmarshal(name, &key); err != nil {
			return nil, 0, fmt.Errorf("decode manifest key %s: %w", name, err)
		}

		value, err := dec.ReadValue()
		if err != nil {
			return nil, 0, fmt.Errorf("read manifest key %q: %w", key, err)
		}
		valueEnd := int(dec.InputOffset())

		members = append(members, manifestMember{
			key:        key,
			nameStart:  nameEnd - len(name),
			valueStart: valueEnd - len(value),
			valueEnd:   valueEnd,
		})
	}
	return members, braceEnd, nil
}

// blockStyle formats a generated value like the manifest it joins: indented
// with the member indentation when the members sit on their own lines,
// compact otherwise.
type blockStyle struct {
	indent     string
	multiline  bool
	defaultGap string
	colon      string
}

func newBlockStyle(lead string) blockStyle {
	_, indent, multiline := strings.CutLast(lead, "\n")
	if !multiline {
		return blockStyle{defaultGap: ",", colon: ":"}
	}
	return blockStyle{indent: indent, multiline: true, defaultGap: "," + lead, colon: ": "}
}

func (s blockStyle) format(v jsontext.Value) ([]byte, error) {
	out := slices.Clone(v)
	if !s.multiline {
		return out, out.Compact()
	}
	return out, out.Indent(jsontext.WithIndentPrefix(s.indent), jsontext.WithIndent(s.indent))
}

// gap returns the separator that followed members[prev] in raw, falling back
// to the style's default for a member that was last.
func (s blockStyle) gap(raw []byte, members []manifestMember, prev int) string {
	if prev+1 >= len(members) {
		return s.defaultGap
	}
	return string(raw[members[prev].valueEnd:members[prev+1].nameStart])
}

// sameJSON reports whether a and b are the same JSON text apart from
// whitespace.
func sameJSON(a, b jsontext.Value) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	return a.Compact() == nil && b.Compact() == nil && bytes.Equal(a, b)
}
