package tui

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// viewMode controls how payloads are shown in the detail pane.
type viewMode int

const (
	viewAuto viewMode = iota // pretty JSON, text, or hex for binary
	viewRaw                  // text exactly as received
	viewHex                  // hex dump
)

func (v viewMode) String() string {
	switch v {
	case viewRaw:
		return "raw"
	case viewHex:
		return "hex"
	default:
		return "auto"
	}
}

// isText reports whether the payload is printable UTF-8.
func isText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if r == utf8.RuneError || (unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t') {
			return false
		}
	}
	return true
}

// isJSON reports whether the payload is a JSON object or array (scalars are
// shown as plain text).
func isJSON(b []byte) bool {
	t := bytes.TrimSpace(b)
	if len(t) == 0 || (t[0] != '{' && t[0] != '[') {
		return false
	}
	return json.Valid(t)
}

// preview renders a payload on a single line for the message list and tree.
func preview(b []byte, max int) string {
	if len(b) == 0 {
		return "(empty)"
	}
	if !isText(b) {
		return fmt.Sprintf("<binary %s>", humanBytes(len(b)))
	}
	if len(b) > 4096 {
		b = b[:4096]
	}
	var sb strings.Builder
	space := false
	n := 0
	for _, r := range string(b) {
		if unicode.IsSpace(r) {
			if !space && n > 0 {
				sb.WriteByte(' ')
				n++
			}
			space = true
			continue
		}
		space = false
		sb.WriteRune(r)
		n++
		if n >= max {
			break
		}
	}
	return sb.String()
}

// formatPayload renders a payload for the detail pane, width cells wide.
func formatPayload(b []byte, mode viewMode, width int) (string, string) {
	if len(b) == 0 {
		return stDim.Render("(empty payload)"), "empty"
	}
	switch {
	case mode == viewHex:
		return hexDump(b, width), "hex"
	case mode == viewAuto && !isText(b):
		return hexDump(b, width), "binary"
	case mode == viewRaw:
		return strings.ToValidUTF8(string(b), "�"), "raw"
	case isJSON(b):
		var buf bytes.Buffer
		if err := json.Indent(&buf, bytes.TrimSpace(b), "", "  "); err == nil {
			return highlightJSON(buf.String()), "json"
		}
	}
	return string(b), "text"
}

// hexDump formats b like hexdump -C, with as many bytes per line (16, 8 or
// 4) as fit in width.
func hexDump(b []byte, width int) string {
	per := 16
	for per > 4 && 10+per*3+per/8+per+2 > width {
		per /= 2
	}
	var sb strings.Builder
	for off := 0; off < len(b); off += per {
		end := min(off+per, len(b))
		line := b[off:end]
		sb.WriteString(stDim.Render(fmt.Sprintf("%08x", off)))
		sb.WriteString("  ")
		for i := 0; i < per; i++ {
			if i > 0 && i%8 == 0 {
				sb.WriteByte(' ')
			}
			if i < len(line) {
				sb.WriteString(hex.EncodeToString(line[i : i+1]))
				sb.WriteByte(' ')
			} else {
				sb.WriteString("   ")
			}
		}
		sb.WriteString(stDim.Render("|"))
		for _, c := range line {
			if c < 32 || c > 126 {
				c = '.'
			}
			sb.WriteByte(c)
		}
		sb.WriteString(stDim.Render("|"))
		if end < len(b) {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// highlightJSON colours indented JSON.
func highlightJSON(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' {
					j += 2
					continue
				}
				if s[j] == '"' {
					break
				}
				j++
			}
			if j >= len(s) {
				j = len(s) - 1
			}
			tok := s[i : j+1]
			k := j + 1
			for k < len(s) && s[k] == ' ' {
				k++
			}
			if k < len(s) && s[k] == ':' {
				out.WriteString(stJSONKey.Render(tok))
			} else {
				out.WriteString(stJSONStr.Render(tok))
			}
			i = j + 1
		case c == '-' || (c >= '0' && c <= '9'):
			j := i
			for j < len(s) && strings.IndexByte("+-0123456789.eE", s[j]) >= 0 {
				j++
			}
			out.WriteString(stJSONNum.Render(s[i:j]))
			i = j
		case strings.HasPrefix(s[i:], "true"), strings.HasPrefix(s[i:], "null"):
			out.WriteString(stJSONLit.Render(s[i : i+4]))
			i += 4
		case strings.HasPrefix(s[i:], "false"):
			out.WriteString(stJSONLit.Render(s[i : i+5]))
			i += 5
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}

func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	}
}

// formatCount renders an integer with thousands separators.
func formatCount(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return s
	}
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
