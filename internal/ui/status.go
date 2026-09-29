package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// App-level messages: a fetch that partly failed, data that went stale, an
// action that completed. Views raise them, the root model renders them in the
// status line, and errors open their own detail overlay so a wrapped GraphQL
// message is readable in full rather than truncated to one row.

// Severity orders messages and picks their colour.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarn
	SeverityError
)

// StatusMsg is one message, raised by a view.
type StatusMsg struct {
	Severity Severity
	Source   string // which view raised it ("PRs", "Linear")
	Summary  string // one line, for the status row
	Detail   string // optional, may wrap; shown in the overlay
	At       time.Time
}

// Status returns the tea.Msg form; the root model appends it to its log.
func Status(sev Severity, source, summary, detail string) StatusMsg {
	return StatusMsg{Severity: sev, Source: source, Summary: summary, Detail: detail, At: time.Now()}
}

func (s StatusMsg) icon() string {
	switch s.Severity {
	case SeverityError:
		return "✗"
	case SeverityWarn:
		return "⚠"
	default:
		return "✓"
	}
}

func (s StatusMsg) style() lipgloss.Style {
	switch s.Severity {
	case SeverityError:
		return Red
	case SeverityWarn:
		return Yellow
	default:
		return Green
	}
}

// Line renders the status row: icon, summary, and a hint when there is more
// to read. Empty when there is nothing to say, so the row costs no height.
func (s StatusMsg) Line(width int, hintKey string) string {
	if s.Summary == "" {
		return ""
	}
	hint := ""
	if s.Detail != "" && hintKey != "" {
		hint = Dim.Render(hintKey + " details")
	}
	body := s.style().Render(s.icon()+" ") + s.Summary
	gap := max(1, width-lipgloss.Width(body)-lipgloss.Width(hint))
	return body + strings.Repeat(" ", gap) + hint
}

// DetailView renders the message log as an overlay, newest first. Bordered
// like the other overlays: without a frame it blends into the pane behind.
func DetailView(msgs []StatusMsg, width int) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(Pal().Accent)).
		Padding(0, 2).
		Render(detailBody(msgs, width))
}

func detailBody(msgs []StatusMsg, width int) string {
	var b strings.Builder
	b.WriteString(Bold.Render("Messages"))
	b.WriteString("\n\n")
	if len(msgs) == 0 {
		b.WriteString(Faint.Render("(nothing to report)"))
		return b.String()
	}
	wrap := max(30, width)
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		fmt.Fprintf(&b, "%s %s  %s\n",
			m.style().Render(m.icon()), Dim.Render(Age(m.At)+" ago"), Bold.Render(m.Source))
		b.WriteString(indentWrap(m.Summary, wrap, "   "))
		if m.Detail != "" {
			b.WriteString(indentWrap(m.Detail, wrap, "   "))
		}
		if i > 0 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// indentWrap wraps s to width and indents every line, for the overlay body.
func indentWrap(s string, width int, indent string) string {
	var b strings.Builder
	for _, para := range strings.Split(s, "\n") {
		line := indent
		for _, word := range strings.Fields(para) {
			if line != indent && lipgloss.Width(line)+1+len(word) > width {
				b.WriteString(line + "\n")
				line = indent
			}
			if line != indent {
				line += " "
			}
			line += word
		}
		if strings.TrimSpace(line) != "" {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
