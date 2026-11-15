package global

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// zonField returns the string value of the top-level field name
// in src, a ZON struct such as the one that "zig env" prints:
//
//	.{
//	    .global_cache_dir = "/home/user/.cache/zig",
//	    .env = .{ .ZIG_GLOBAL_CACHE_DIR = null },
//	}
//
// It reads only what it needs: field names, string literals,
// and enough of other values to skip them.
func zonField(src, name string) (string, error) {
	p := &zonParser{s: src}
	p.space()
	if !p.take(".{") {
		return "", errors.New("output is not a ZON struct")
	}
	for {
		p.space()
		if p.take("}") {
			return "", fmt.Errorf("output has no %s field", name)
		}
		if !p.take(".") {
			return "", p.errorf("expected a field")
		}
		field, err := p.ident()
		if err != nil {
			return "", err
		}
		p.space()
		if !p.take("=") {
			return "", p.errorf("expected = after .%s", field)
		}
		p.space()
		if field == name {
			if !strings.HasPrefix(p.s[p.i:], `"`) {
				return "", fmt.Errorf("field %s is not a string", name)
			}
			return p.str()
		}
		if err := p.skipValue(); err != nil {
			return "", err
		}
		p.space()
		p.take(",")
	}
}

type zonParser struct {
	s string
	i int
}

func (p *zonParser) errorf(format string, args ...any) error {
	return fmt.Errorf("cannot read ZON at byte %d: %s", p.i, fmt.Sprintf(format, args...))
}

func (p *zonParser) take(prefix string) bool {
	if strings.HasPrefix(p.s[p.i:], prefix) {
		p.i += len(prefix)
		return true
	}
	return false
}

// space skips whitespace and // comments.
func (p *zonParser) space() {
	for p.i < len(p.s) {
		switch {
		case strings.ContainsRune(" \t\r\n", rune(p.s[p.i])):
			p.i++
		case p.take("//"):
			p.line()
		default:
			return
		}
	}
}

// line skips to the end of the line.
func (p *zonParser) line() {
	if j := strings.IndexByte(p.s[p.i:], '\n'); j >= 0 {
		p.i += j
	} else {
		p.i = len(p.s)
	}
}

// ident reads a field name, plain or written as @"...".
func (p *zonParser) ident() (string, error) {
	if p.take("@") {
		if !strings.HasPrefix(p.s[p.i:], `"`) {
			return "", p.errorf(`expected " after @`)
		}
		return p.str()
	}
	j := p.i
	for j < len(p.s) {
		c := p.s[j]
		if c != '_' && !('a' <= c && c <= 'z') && !('A' <= c && c <= 'Z') && !('0' <= c && c <= '9') {
			break
		}
		j++
	}
	if j == p.i {
		return "", p.errorf("expected a field name")
	}
	name := p.s[p.i:j]
	p.i = j
	return name, nil
}

// str reads a string literal and decodes Zig's escape sequences.
func (p *zonParser) str() (string, error) {
	p.i++ // the opening quote
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch c {
		case '"':
			p.i++
			return b.String(), nil
		case '\n':
			return "", p.errorf("string has a line break")
		case '\\':
			if err := p.escape(&b); err != nil {
				return "", err
			}
		default:
			b.WriteByte(c)
			p.i++
		}
	}
	return "", p.errorf("string is not closed")
}

func (p *zonParser) escape(b *strings.Builder) error {
	if p.i+1 >= len(p.s) {
		return p.errorf("string is not closed")
	}
	c := p.s[p.i+1]
	p.i += 2
	switch c {
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case '\\', '\'', '"':
		b.WriteByte(c)
	case 'x':
		if p.i+2 > len(p.s) {
			return p.errorf(`\x needs two hex digits`)
		}
		n, err := strconv.ParseUint(p.s[p.i:p.i+2], 16, 8)
		if err != nil {
			return p.errorf(`\x needs two hex digits`)
		}
		b.WriteByte(byte(n))
		p.i += 2
	case 'u':
		end := strings.IndexByte(p.s[p.i:], '}')
		if !strings.HasPrefix(p.s[p.i:], "{") || end < 2 {
			return p.errorf(`\u needs {hex digits}`)
		}
		n, err := strconv.ParseUint(p.s[p.i+1:p.i+end], 16, 32)
		if err != nil || !utf8.ValidRune(rune(n)) {
			return p.errorf(`\u has an invalid code point`)
		}
		b.WriteRune(rune(n))
		p.i += end + 1
	default:
		return p.errorf(`unknown escape \%c`, c)
	}
	return nil
}

// skipValue skips a value up to the comma or brace that ends it.
func (p *zonParser) skipValue() error {
	depth := 0
	for p.i < len(p.s) {
		switch c := p.s[p.i]; {
		case c == '"':
			if _, err := p.str(); err != nil {
				return err
			}
			continue
		case c == '\'':
			// A character literal, such as '}' or '\''.
			p.i++
			for p.i < len(p.s) && p.s[p.i] != '\'' {
				if p.s[p.i] == '\\' {
					p.i++
				}
				p.i++
			}
		case p.take(`\\`), p.take("//"):
			// A multiline string line or a comment runs to the end of the line.
			p.line()
			continue
		case c == '{':
			depth++
		case c == '}':
			if depth == 0 {
				return nil
			}
			depth--
		case c == ',' && depth == 0:
			return nil
		}
		p.i++
	}
	return p.errorf("struct is not closed")
}

// zonPath reads a path from field of the ZON struct in out.
// Zig prints a path inside the working directory relative to it,
// so a relative path is joined to home, where plonge runs the command.
func zonPath(out, field, home string) (string, error) {
	path, err := zonField(out, field)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("printed an empty %s", field)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(home, path)
	}
	return filepath.Clean(path), nil
}
