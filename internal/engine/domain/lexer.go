// Package domain implements the domain expression language shared across
// GoERP: ABAC policy conditions, host.orm.search domain arguments, and
// (in other contexts, not this package) shell-interpreted view/field
// conditions. See manifest-spec.md §8 for the grammar.
package domain

import (
	"fmt"
	"strings"
	"unicode"
)

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokNumber
	tokDot
	tokLParen
	tokRParen
	tokComma
	tokEq
	tokNeq
	tokLt
	tokGt
	tokLte
	tokGte

	// keywords
	tokAnd
	tokOr
	tokNot
	tokIn
	tokIs
	tokNull
	tokTrue
	tokFalse
	tokLike
	tokIlike
	tokChildOf
	tokParentOf
)

var keywords = map[string]tokenKind{
	"and":       tokAnd,
	"or":        tokOr,
	"not":       tokNot,
	"in":        tokIn,
	"is":        tokIs,
	"null":      tokNull,
	"true":      tokTrue,
	"false":     tokFalse,
	"like":      tokLike,
	"ilike":     tokIlike,
	"child_of":  tokChildOf,
	"parent_of": tokParentOf,
}

type token struct {
	kind tokenKind
	text string
	pos  int
}

type lexer struct {
	src string
	pos int
}

func newLexer(src string) *lexer {
	return &lexer{src: src}
}

func (l *lexer) tokenize() ([]token, error) {
	var toks []token
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		toks = append(toks, tok)
		if tok.kind == tokEOF {
			return toks, nil
		}
	}
}

func (l *lexer) next() (token, error) {
	l.skipSpace()
	if l.pos >= len(l.src) {
		return token{kind: tokEOF, pos: l.pos}, nil
	}

	start := l.pos
	c := l.src[l.pos]

	switch {
	case c == '\'':
		return l.lexString()
	case c == '.':
		l.pos++
		return token{kind: tokDot, text: ".", pos: start}, nil
	case c == '(':
		l.pos++
		return token{kind: tokLParen, text: "(", pos: start}, nil
	case c == ')':
		l.pos++
		return token{kind: tokRParen, text: ")", pos: start}, nil
	case c == ',':
		l.pos++
		return token{kind: tokComma, text: ",", pos: start}, nil
	case c == '=':
		l.pos++
		return token{kind: tokEq, text: "=", pos: start}, nil
	case c == '!':
		if l.peekAt(1) == '=' {
			l.pos += 2
			return token{kind: tokNeq, text: "!=", pos: start}, nil
		}
		return token{}, fmt.Errorf("domain: unexpected character %q at position %d", c, start)
	case c == '<':
		if l.peekAt(1) == '=' {
			l.pos += 2
			return token{kind: tokLte, text: "<=", pos: start}, nil
		}
		l.pos++
		return token{kind: tokLt, text: "<", pos: start}, nil
	case c == '>':
		if l.peekAt(1) == '=' {
			l.pos += 2
			return token{kind: tokGte, text: ">=", pos: start}, nil
		}
		l.pos++
		return token{kind: tokGt, text: ">", pos: start}, nil
	case isDigit(c):
		return l.lexNumber()
	case isIdentStart(c):
		return l.lexIdent()
	default:
		return token{}, fmt.Errorf("domain: unexpected character %q at position %d", c, start)
	}
}

func (l *lexer) lexString() (token, error) {
	start := l.pos
	l.pos++ // consume opening quote
	var sb strings.Builder
	for {
		if l.pos >= len(l.src) {
			return token{}, fmt.Errorf("domain: unterminated string literal starting at position %d", start)
		}
		c := l.src[l.pos]
		if c == '\'' {
			// SQL-style escaping: '' inside a string is a literal quote.
			if l.peekAt(1) == '\'' {
				sb.WriteByte('\'')
				l.pos += 2
				continue
			}
			l.pos++
			return token{kind: tokString, text: sb.String(), pos: start}, nil
		}
		sb.WriteByte(c)
		l.pos++
	}
}

func (l *lexer) lexNumber() (token, error) {
	start := l.pos
	for l.pos < len(l.src) && (isDigit(l.src[l.pos]) || l.src[l.pos] == '.') {
		l.pos++
	}
	return token{kind: tokNumber, text: l.src[start:l.pos], pos: start}, nil
}

func (l *lexer) lexIdent() (token, error) {
	start := l.pos
	for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
		l.pos++
	}
	text := l.src[start:l.pos]
	if kind, ok := keywords[strings.ToLower(text)]; ok {
		return token{kind: kind, text: text, pos: start}, nil
	}
	return token{kind: tokIdent, text: text, pos: start}, nil
}

func (l *lexer) skipSpace() {
	for l.pos < len(l.src) && unicode.IsSpace(rune(l.src[l.pos])) {
		l.pos++
	}
}

func (l *lexer) peekAt(offset int) byte {
	if l.pos+offset >= len(l.src) {
		return 0
	}
	return l.src[l.pos+offset]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}
