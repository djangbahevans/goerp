package codegen

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type NameRef struct {
	File   string
	Line   int
	Helper string
	Name   string
}

// ScanNameRefs scans frontend/src, excluding the generated output. Only
// literal arguments are checked; computed names remain runtime values.
func ScanNameRefs(dir, skipFile string) ([]NameRef, error) {
	root := filepath.Join(dir, "frontend", "src")
	skipAbs, err := filepath.Abs(skipFile)
	if err != nil {
		return nil, fmt.Errorf("resolve output path: %w", err)
	}

	var refs []NameRef
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && SkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !IsFrontendSourceFile(d.Name()) {
			return nil
		}
		if abs, err := filepath.Abs(path); err == nil && abs == skipAbs {
			return nil
		}
		found, err := scanFileNameRefs(path)
		if err != nil {
			return err
		}
		refs = append(refs, found...)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan frontend sources for name references: %w", err)
	}
	return refs, nil
}

// Quoted text and comments are single tokens so helper-like text inside
// them cannot be mistaken for a call. Arrow types must not close generics.
var frontendTokenPattern = regexp.MustCompile("(?s)//[^\\r\\n]*|/\\*.*?\\*/|'(?:\\\\.|[^'\\\\])*'|\"(?:\\\\.|[^\"\\\\])*\"|[a-zA-Z_$][a-zA-Z0-9_$]*|=>|[^\\s]")

type frontendToken struct {
	text string
	line int
}

func scanFileNameRefs(path string) ([]NameRef, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	tokens, _ := frontendTokens(string(raw), 1, false)

	var refs []NameRef
	for i, token := range tokens {
		switch token.text {
		case "permissionRef", "resourceRef", "actionRef":
		default:
			continue
		}

		arg := i + 1
		if arg < len(tokens) && tokens[arg].text == "<" {
			depth := 0
			for ; arg < len(tokens); arg++ {
				switch tokens[arg].text {
				case "<":
					depth++
				case ">":
					depth--
				}
				if depth == 0 {
					arg++
					break
				}
			}
		}
		if arg+2 >= len(tokens) || tokens[arg].text != "(" {
			continue
		}
		arg++
		if tokens[arg+1].text != ")" && tokens[arg+1].text != "," {
			continue
		}
		name, literal := refLiteral(tokens[arg].text)
		if literal {
			refs = append(refs, NameRef{File: path, Line: token.line, Helper: token.text, Name: name})
		}
	}
	return refs, nil
}

// Interpolations contain executable expressions, so nested helper calls
// must remain visible even though the containing template is dynamic.
func frontendTokens(source string, line int, interpolation bool) ([]frontendToken, int) {
	var tokens []frontendToken
	end, depth := 0, 0
	for end < len(source) {
		match := frontendTokenPattern.FindStringIndex(source[end:])
		if match == nil {
			break
		}
		start := end + match[0]
		line += strings.Count(source[end:start], "\n")
		end = end + match[1]
		token := source[start:end]
		if token == "`" {
			template, length := templateTokens(source[start:], line)
			tokens = append(tokens, template...)
			end = start + length
		} else if !strings.HasPrefix(token, "//") && !strings.HasPrefix(token, "/*") {
			if interpolation {
				switch token {
				case "{":
					depth++
				case "}":
					if depth == 0 {
						return tokens, end
					}
					depth--
				}
			}
			tokens = append(tokens, frontendToken{text: token, line: line})
		}
		line += strings.Count(source[start:end], "\n")
	}
	return tokens, end
}

func templateTokens(source string, line int) ([]frontendToken, int) {
	startLine := line
	var expressions []frontendToken
	dynamic := false
	end := 1
	for end < len(source) {
		switch {
		case source[end] == '\\':
			length := min(2, len(source)-end)
			line += strings.Count(source[end:end+length], "\n")
			end += length
		case source[end] == '`':
			end++
			if !dynamic {
				return []frontendToken{{text: source[:end], line: startLine}}, end
			}
			return append([]frontendToken{{text: "template", line: startLine}}, expressions...), end
		case strings.HasPrefix(source[end:], "${"):
			dynamic = true
			end += 2
			tokens, length := frontendTokens(source[end:], line, true)
			expressions = append(expressions, tokens...)
			line += strings.Count(source[end:end+length], "\n")
			end += length
		default:
			if source[end] == '\n' {
				line++
			}
			end++
		}
	}
	return []frontendToken{{text: "template", line: startLine}}, end
}

func refLiteral(token string) (string, bool) {
	if len(token) < 2 || (token[0] != '\'' && token[0] != '"' && token[0] != '`') {
		return "", false
	}
	body := token[1 : len(token)-1]
	var name strings.Builder
	for body != "" {
		char, length := utf8.DecodeRuneInString(body)
		if char == '\\' {
			body = body[length:]
			if body == "" {
				return "", false
			}
			char, length = utf8.DecodeRuneInString(body)
			switch char {
			case '\r':
				body = strings.TrimPrefix(body[length:], "\n")
				continue
			case '\n':
				body = body[length:]
				continue
			case 'b':
				char = '\b'
			case 'f':
				char = '\f'
			case 'n':
				char = '\n'
			case 'r':
				char = '\r'
			case 't':
				char = '\t'
			case 'v':
				char = '\v'
			case '0':
				char = 0
			case 'x', 'u':
				digits := 2
				if char == 'u' {
					digits = 4
				}
				hex := body[1:]
				length += digits
				if char == 'u' && strings.HasPrefix(hex, "{") {
					close := strings.IndexByte(hex, '}')
					if close < 0 {
						return "", false
					}
					hex = hex[1:close]
					length = close + 2
				} else {
					if len(hex) < digits {
						return "", false
					}
					hex = hex[:digits]
				}
				value, err := strconv.ParseUint(hex, 16, 32)
				if err != nil || value > utf8.MaxRune {
					return "", false
				}
				char = rune(value)
			}
		}
		name.WriteRune(char)
		body = body[length:]
	}
	return name.String(), true
}
