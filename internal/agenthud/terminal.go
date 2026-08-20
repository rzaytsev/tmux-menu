package agenthud

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const (
	defaultTextInputBytes = 16 << 10
	defaultTextBytes      = 4 << 10
	tabWidth              = 4
)

type TerminalLimits struct {
	Width            int
	Height           int
	MaxInputBytes    int
	MaxRetainedBytes int
}

// Text is printable display data. Its internal spans can only be constructed by
// the sanitizer, so HUD model state never needs to retain untrusted raw strings.
type Text struct {
	spans []span
	width int
	bytes int
}

type Terminal struct {
	lines []Text
	bytes int
}

type span struct {
	text  string
	style style
}

type style struct {
	bold          bool
	dim           bool
	italic        bool
	underline     bool
	reverse       bool
	foreground    int
	foreground256 int
	background    int
}

func SanitizeText(raw string, width int) Text {
	if width <= 0 {
		return Text{}
	}
	parsed := parseTerminal([]byte(raw), defaultTextInputBytes, true)
	if len(parsed) == 0 {
		return Text{}
	}
	return clipLine(parsed[0], width, defaultTextBytes)
}

func SanitizeTerminal(raw []byte, limits TerminalLimits) Terminal {
	if limits.Width <= 0 || limits.Height <= 0 || limits.MaxInputBytes <= 0 || limits.MaxRetainedBytes <= 0 {
		return Terminal{}
	}
	if len(raw) > limits.MaxInputBytes {
		raw = raw[:limits.MaxInputBytes]
	}
	parsed := parseTerminal(raw, limits.MaxInputBytes, false)
	if len(parsed) > limits.Height {
		parsed = parsed[len(parsed)-limits.Height:]
	}

	terminal := Terminal{lines: make([]Text, 0, len(parsed))}
	remaining := limits.MaxRetainedBytes
	for _, rawLine := range parsed {
		line := clipLine(rawLine, limits.Width, remaining)
		terminal.lines = append(terminal.lines, line)
		terminal.bytes += line.bytes
		remaining -= line.bytes
		if remaining < 0 {
			remaining = 0
		}
	}
	return terminal
}

func (t Text) Plain() string {
	var out strings.Builder
	out.Grow(t.bytes)
	for _, part := range t.spans {
		out.WriteString(part.text)
	}
	return out.String()
}

func (t Text) ANSI() string {
	var out strings.Builder
	out.WriteString("\x1b[0m")
	current := style{}
	for _, part := range t.spans {
		if part.style != current {
			out.WriteString(part.style.ansi())
			current = part.style
		}
		out.WriteString(part.text)
	}
	out.WriteString("\x1b[0m")
	return out.String()
}

func (t Text) Width() int {
	return t.width
}

func (t Text) RetainedBytes() int {
	return t.bytes
}

func (t Terminal) Plain() string {
	lines := make([]string, len(t.lines))
	for i, line := range t.lines {
		lines[i] = line.Plain()
	}
	return strings.Join(lines, "\n")
}

func (t Terminal) ANSI() string {
	if len(t.lines) == 0 {
		return "\x1b[0m"
	}
	lines := make([]string, len(t.lines))
	for i, line := range t.lines {
		lines[i] = line.ANSI()
	}
	return strings.Join(lines, "\n")
}

func (t Terminal) LineCount() int {
	return len(t.lines)
}

func (t Terminal) RetainedBytes() int {
	return t.bytes
}

func parseTerminal(raw []byte, maxInput int, flatten bool) [][]span {
	if len(raw) > maxInput {
		raw = raw[:maxInput]
	}
	lines := [][]span{{}}
	current := style{}
	lineWidth := 0
	for i := 0; i < len(raw); {
		b := raw[i]
		switch {
		case b == 0x1b:
			next, sgr, ok := consumeEscape(raw, i)
			if ok {
				current = applySGR(current, sgr)
			}
			i = next
		case b >= 0x80 && b <= 0x9f:
			i++
		case b == '\n':
			if flatten {
				appendParsed(&lines[len(lines)-1], " ", current)
				lineWidth++
			} else {
				lines = append(lines, nil)
				lineWidth = 0
			}
			i++
		case b == '\t':
			spaces := tabWidth - lineWidth%tabWidth
			appendParsed(&lines[len(lines)-1], strings.Repeat(" ", spaces), current)
			lineWidth += spaces
			i++
		case b < 0x20 || b == 0x7f:
			i++
		default:
			r, size := utf8.DecodeRune(raw[i:])
			if r == utf8.RuneError && size == 1 {
				r = utf8.RuneError
			}
			i += size
			if isUnsafeRune(r) {
				continue
			}
			text := string(r)
			appendParsed(&lines[len(lines)-1], text, current)
			lineWidth += ansi.StringWidth(text)
		}
	}
	return lines
}

func appendParsed(parts *[]span, text string, value style) {
	if text == "" {
		return
	}
	last := len(*parts) - 1
	if last >= 0 && (*parts)[last].style == value {
		(*parts)[last].text += text
		return
	}
	*parts = append(*parts, span{text: text, style: value})
}

func clipLine(parts []span, maxWidth, maxBytes int) Text {
	if maxWidth <= 0 || maxBytes <= 0 {
		return Text{}
	}
	var result Text
	for _, part := range parts {
		for remaining := part.text; remaining != ""; {
			cluster, width := ansi.FirstGraphemeCluster(remaining, ansi.GraphemeWidth)
			if cluster == "" {
				break
			}
			if result.width+width > maxWidth || result.bytes+len(cluster) > maxBytes {
				return result
			}
			appendParsed(&result.spans, cluster, part.style)
			result.width += width
			result.bytes += len(cluster)
			remaining = remaining[len(cluster):]
		}
	}
	return result
}

func consumeEscape(raw []byte, start int) (next int, sgr []int, passive bool) {
	if start+1 >= len(raw) {
		return len(raw), nil, false
	}
	switch raw[start+1] {
	case '[':
		for i := start + 2; i < len(raw); i++ {
			if raw[i] < 0x40 || raw[i] > 0x7e {
				continue
			}
			if raw[i] != 'm' {
				return i + 1, nil, false
			}
			params, ok := parseSGR(raw[start+2 : i])
			return i + 1, params, ok
		}
		return len(raw), nil, false
	case ']':
		return consumeStringControl(raw, start+2, true), nil, false
	case 'P', '_', '^', 'X':
		return consumeStringControl(raw, start+2, false), nil, false
	default:
		return min(start+2, len(raw)), nil, false
	}
}

func consumeStringControl(raw []byte, start int, bellTerminates bool) int {
	for i := start; i < len(raw); i++ {
		if bellTerminates && raw[i] == '\a' {
			return i + 1
		}
		if raw[i] == 0x1b && i+1 < len(raw) && raw[i+1] == '\\' {
			return i + 2
		}
	}
	return len(raw)
}

func parseSGR(raw []byte) ([]int, bool) {
	if len(raw) == 0 {
		return []int{0}, true
	}
	for _, b := range raw {
		if (b < '0' || b > '9') && b != ';' {
			return nil, false
		}
	}
	fields := strings.Split(string(raw), ";")
	params := make([]int, len(fields))
	for i, field := range fields {
		if field == "" {
			params[i] = 0
			continue
		}
		value, err := strconv.Atoi(field)
		if err != nil {
			return nil, false
		}
		params[i] = value
	}
	return params, true
}

func applySGR(current style, params []int) style {
	for _, param := range params {
		switch {
		case param == 0:
			current = style{}
		case param == 1:
			current.bold = true
		case param == 2:
			current.dim = true
		case param == 3:
			current.italic = true
		case param == 4:
			current.underline = true
		case param == 7:
			current.reverse = true
		case param == 22:
			current.bold = false
			current.dim = false
		case param == 23:
			current.italic = false
		case param == 24:
			current.underline = false
		case param == 27:
			current.reverse = false
		case param >= 30 && param <= 37, param >= 90 && param <= 97:
			current.foreground = param
		case param == 39:
			current.foreground = 0
			current.foreground256 = 0
		case param >= 40 && param <= 47, param >= 100 && param <= 107:
			current.background = param
		case param == 49:
			current.background = 0
		}
	}
	return current
}

func (s style) ansi() string {
	codes := make([]string, 0, 7)
	if s.bold {
		codes = append(codes, "1")
	}
	if s.dim {
		codes = append(codes, "2")
	}
	if s.italic {
		codes = append(codes, "3")
	}
	if s.underline {
		codes = append(codes, "4")
	}
	if s.reverse {
		codes = append(codes, "7")
	}
	if s.foreground != 0 {
		codes = append(codes, strconv.Itoa(s.foreground))
	}
	if s.foreground256 != 0 {
		codes = append(codes, "38", "5", strconv.Itoa(s.foreground256))
	}
	if s.background != 0 {
		codes = append(codes, strconv.Itoa(s.background))
	}
	if len(codes) == 0 {
		return "\x1b[0m"
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

func isUnsafeRune(r rune) bool {
	return (r >= 0x80 && r <= 0x9f) ||
		r == 0x061c || r == 0x200e || r == 0x200f ||
		(r >= 0x202a && r <= 0x202e) ||
		(r >= 0x2066 && r <= 0x2069)
}
