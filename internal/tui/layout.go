package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// fit pads or truncates a (possibly styled) line to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lw := ansi.StringWidth(s); lw > w {
		return ansi.Truncate(s, w, "…")
	} else if lw < w {
		return s + strings.Repeat(" ", w-lw)
	}
	return s
}

// box draws a rounded border of size w×h around lines, with a title (and an
// optional right-aligned note) in the top edge.
func box(title, note string, lines []string, w, h int, focused bool) string {
	if w < 4 || h < 2 {
		return ""
	}
	border := stDim
	titleStyle := stMuted
	if focused {
		border = stAccent
		titleStyle = stAccent.Bold(true)
	}
	inner := w - 2

	top := border.Render("╭─")
	used := 2
	if title != "" {
		t := ansi.Truncate(" "+title+" ", inner-2, "…")
		top += titleStyle.Render(t)
		used += ansi.StringWidth(t)
	}
	noteStr := ""
	if note != "" {
		noteStr = " " + note + " "
		if used+ansi.StringWidth(noteStr)+2 > w {
			noteStr = ""
		}
	}
	fill := w - used - ansi.StringWidth(noteStr) - 2
	if fill < 0 {
		fill = 0
	}
	top += border.Render(strings.Repeat("─", fill))
	if noteStr != "" {
		top += stMuted.Render(noteStr)
	}
	top += border.Render("─╮")

	var b strings.Builder
	b.WriteString(top)
	side := border.Render("│")
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		b.WriteByte('\n')
		b.WriteString(side)
		b.WriteString(fit(line, inner))
		b.WriteString(side)
	}
	b.WriteByte('\n')
	b.WriteString(border.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

// overlay draws fg on top of bg with its top-left corner at (x, y).
func overlay(bg, fg string, x, y int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")
	for i, fl := range fgLines {
		row := y + i
		if row < 0 || row >= len(bgLines) {
			continue
		}
		bl := bgLines[row]
		fw := ansi.StringWidth(fl)
		left := ansi.Truncate(bl, x, "")
		if lw := ansi.StringWidth(left); lw < x {
			left += strings.Repeat(" ", x-lw)
		}
		right := ""
		if ansi.StringWidth(bl) > x+fw {
			right = ansi.TruncateLeft(bl, x+fw, "")
		}
		bgLines[row] = left + "\x1b[0m" + fl + "\x1b[0m" + right
	}
	return strings.Join(bgLines, "\n")
}

// center overlays fg in the middle of a w×h background.
func center(bg, fg string, w, h int) string {
	fw := lipgloss.Width(fg)
	fh := lipgloss.Height(fg)
	x := (w - fw) / 2
	y := (h - fh) / 3
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return overlay(bg, fg, x, y)
}

// windowStart returns the first index to show so that cursor is visible in a
// window of the given height, keeping the previous offset where possible.
func windowStart(offset, cursor, height, total int) int {
	if height <= 0 {
		return 0
	}
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+height {
		offset = cursor - height + 1
	}
	if max := total - height; offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}
