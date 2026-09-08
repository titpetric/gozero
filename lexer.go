package gozero

import (
	"fmt"
	"strconv"
)

func joinPath(path []string) string {
	out := ""
	for i, s := range path {
		if i > 0 {
			out += "."
		}
		out += s
	}
	return out
}

func (p *Parser) stringLit(quote byte) (arg, error) {
	p.pos++ // opening quote
	start := p.pos
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case quote:
			// No escapes: the literal is a substring of the source,
			// zero-copy.
			lit := p.src[start:p.pos]
			p.pos++
			p.nl = false
			return arg{kind: argString, str: lit}, nil
		case '\\':
			return p.stringLitEscaped(quote, start)
		}
		p.pos++
	}
	return arg{}, fmt.Errorf("parse: unterminated string at offset %d", p.pos)
}

// stringLitEscaped is the slow path taken at the first backslash: the
// literal needs unescaping into a buffer.
func (p *Parser) stringLitEscaped(quote byte, start int) (arg, error) {
	buf := append([]byte(nil), p.src[start:p.pos]...)
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch c {
		case quote:
			p.pos++
			p.nl = false
			return arg{kind: argString, str: string(buf)}, nil
		case '\\':
			if p.pos+1 >= len(p.src) {
				return arg{}, fmt.Errorf("parse: unterminated escape at offset %d", p.pos)
			}
			p.pos++
			// The named escapes are interpreted; an unrecognised one is
			// an error rather than the backslash being dropped, which
			// silently turned "a\nb" into "anb".
			switch e := p.src[p.pos]; e {
			case 'n':
				buf = append(buf, '\n')
			case 't':
				buf = append(buf, '\t')
			case 'r':
				buf = append(buf, '\r')
			case '\\', '"', '\'':
				buf = append(buf, e)
			default:
				return arg{}, fmt.Errorf("parse: unknown escape \\%c at offset %d", e, p.pos)
			}
		default:
			buf = append(buf, c)
		}
		p.pos++
	}
	return arg{}, fmt.Errorf("parse: unterminated string at offset %d", p.pos)
}

func (p *Parser) numberLit() (arg, error) {
	start := p.pos
	if p.src[p.pos] == '-' {
		p.pos++
	}
	float := false
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '.' && !float {
			float = true
			p.pos++
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		p.pos++
	}
	lit := p.src[start:p.pos]
	if p.pos > start {
		p.nl = false
	}
	if float {
		f, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			return arg{}, fmt.Errorf("parse: bad float %q: %w", lit, err)
		}
		return arg{kind: argFloat, f: f}, nil
	}
	i, err := strconv.ParseInt(lit, 10, 64)
	if err != nil {
		return arg{}, fmt.Errorf("parse: bad int %q: %w", lit, err)
	}
	return arg{kind: argInt, i: i}, nil
}

func (p *Parser) keyword(kw string) bool {
	p.skipSpace()
	end := p.pos + len(kw)
	if end > len(p.src) || p.src[p.pos:end] != kw {
		return false
	}
	if end < len(p.src) && isIdentChar(p.src[end]) {
		return false
	}
	p.pos = end
	p.nl = false
	return true
}

func (p *Parser) ident() string {
	p.skipSpace()
	start := p.pos
	for p.pos < len(p.src) && isIdentChar(p.src[p.pos]) {
		p.pos++
	}
	if p.pos > start {
		p.nl = false
	}
	return p.src[start:p.pos]
}

func (p *Parser) consume(c byte) bool {
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == c {
		p.pos++
		p.nl = false
		return true
	}
	return false
}

func (p *Parser) consumeStr(s string) bool {
	p.skipSpace()
	end := p.pos + len(s)
	if end > len(p.src) || p.src[p.pos:end] != s {
		return false
	}
	p.pos = end
	p.nl = false
	return true
}

// peek returns the next non-space byte without consuming it, or 0 at
// the end of the source.
func (p *Parser) peek() byte {
	save, saveNL := p.pos, p.nl
	p.skipSpace()
	c := byte(0)
	if p.pos < len(p.src) {
		c = p.src[p.pos]
	}
	p.pos, p.nl = save, saveNL
	return c
}

func (p *Parser) skipSpace() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '\n':
			p.nl = true
			p.pos++
		case ' ', '\t', '\r':
			p.pos++
		case '/':
			// A line comment runs to the newline. There is no block
			// form.
			if p.pos+1 >= len(p.src) || p.src[p.pos+1] != '/' {
				return
			}
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}

func isIdentChar(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
