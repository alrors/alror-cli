// Package ui is the CLI's visual language: the same mint-on-graphite palette,
// LED meters and paper receipts as the Alror website. Colours degrade
// automatically (NO_COLOR, pipes, dumb terminals) through lipgloss.
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// Palette.
var (
	Accent = lipgloss.Color("#35E08F")
	Warn   = lipgloss.Color("#F5A524")
	Bad    = lipgloss.Color("#FF5A4F")
	Fg     = lipgloss.Color("#F2F2F3")
	Muted  = lipgloss.Color("#A1A1AA")
	Faint  = lipgloss.Color("#6B6B74")
	Line   = lipgloss.Color("#2D2D32")
	Paper  = lipgloss.Color("#EFEDE6")
	Ink    = lipgloss.Color("#17171A")
)

// Text styles.
var (
	Bold    = lipgloss.NewStyle().Bold(true).Foreground(Fg)
	Dim     = lipgloss.NewStyle().Foreground(Muted)
	Fainter = lipgloss.NewStyle().Foreground(Faint)
	Green   = lipgloss.NewStyle().Foreground(Accent)
	Amber   = lipgloss.NewStyle().Foreground(Warn)
	Red     = lipgloss.NewStyle().Foreground(Bad)
	Code    = lipgloss.NewStyle().Foreground(Accent)
)

// Logo is the gate mark (an arch with a change passing through) and the wordmark.
func Logo() string {
	return Bold.Render("∩") + Green.Render("·") + " " + Bold.Render("alror")
}

// bigName is "ALROR" in block letters (ANSI Shadow style).
var bigName = []string{
	" █████╗ ██╗     ██████╗  ██████╗ ██████╗ ",
	"██╔══██╗██║     ██╔══██╗██╔═══██╗██╔══██╗",
	"███████║██║     ██████╔╝██║   ██║██████╔╝",
	"██╔══██║██║     ██╔══██╗██║   ██║██╔══██╗",
	"██║  ██║███████╗██║  ██║╚██████╔╝██║  ██║",
	"╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═╝",
}

// BigName renders the block-letter wordmark with the website's top-to-bottom
// fade (white to grey); the letter shadows are drawn dimmer so the face reads.
func BigName() string {
	fade := []lipgloss.Color{"#F2F2F3", "#D9D9DE", "#BDBDC4", "#A1A1AA", "#85858E", "#6B6B74"}
	shadow := lipgloss.NewStyle().Foreground(lipgloss.Color("#3A3A40"))
	out := make([]string, len(bigName))
	for i, line := range bigName {
		face := lipgloss.NewStyle().Foreground(fade[i]).Bold(true)
		// Style runs, not single characters: fewer escape codes and no seams
		// between glyphs in renderers that draw each styled span separately.
		var b strings.Builder
		var run []rune
		kind := -1 // 0 face, 1 space, 2 shadow
		flush := func() {
			switch kind {
			case 0:
				b.WriteString(face.Render(string(run)))
			case 1:
				b.WriteString(string(run))
			case 2:
				b.WriteString(shadow.Render(string(run)))
			}
			run = run[:0]
		}
		for _, r := range line {
			k := 2
			switch r {
			case '█':
				k = 0
			case ' ':
				k = 1
			}
			if k != kind {
				flush()
				kind = k
			}
			run = append(run, r)
		}
		flush()
		out[i] = b.String()
	}
	return strings.Join(out, "\n")
}

// Banner is shown by `alror` with no arguments.
func Banner(version string) string {
	body := lipgloss.JoinVertical(lipgloss.Left,
		BigName(),
		"",
		Bold.Render("Ship every change at the speed of AI, safely.")+"  "+Fainter.Render("v"+version),
		Dim.Render("Risk-scored, progressively rolled out, verified, auto-reversed."),
	)
	return Box(body, "")
}

// Section prints a mono uppercase label, like the website's section tags.
func Section(label string) string {
	return Fainter.Render(strings.ToUpper(label))
}

// Box draws a rounded panel with an optional title.
func Box(content, title string) string {
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(Line).Padding(1, 2)
	if title != "" {
		content = Section(title) + "\n\n" + content
	}
	return style.Render(content)
}

// Meter renders a risk score as a segmented LED bar.
func Meter(score, segments int) string {
	lit := score * segments / 100
	var b strings.Builder
	for i := 0; i < segments; i++ {
		pos := i * 100 / segments
		switch {
		case i >= lit:
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#26262B")).Render("▌"))
		case pos >= 70:
			b.WriteString(Red.Render("▌"))
		case pos >= 35:
			b.WriteString(Amber.Render("▌"))
		default:
			b.WriteString(Green.Render("▌"))
		}
	}
	return b.String()
}

// LevelStyle colours a risk level.
func LevelStyle(l domain.Level) lipgloss.Style {
	switch l {
	case domain.LevelHigh:
		return Red
	case domain.LevelMedium:
		return Amber
	default:
		return Green
	}
}

// Status renders a deployment status with an LED dot.
func Status(s domain.Status, weight int) string {
	switch s {
	case domain.StatusPromoted:
		return Green.Render("●") + " " + Dim.Render("Promoted")
	case domain.StatusRolledBack:
		return Red.Render("● Rolled back")
	case domain.StatusFailed:
		return Red.Render("✗ Failed")
	case domain.StatusRolling:
		return Green.Render(fmt.Sprintf("● Rolling %d%%", weight))
	default:
		return Fainter.Render("○ Pending")
	}
}

// Stages draws the rollout plan as pips; current is highlighted, earlier are done.
func Stages(p domain.Plan, current int, status domain.Status) string {
	parts := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		label := fmt.Sprintf(" %d%% ", s.Weight)
		switch {
		case status == domain.StatusRolledBack && i == current:
			parts[i] = lipgloss.NewStyle().Foreground(Ink).Background(Bad).Render(label)
		case i < current || status == domain.StatusPromoted:
			parts[i] = Green.Render(label)
		case i == current && status == domain.StatusRolling:
			parts[i] = lipgloss.NewStyle().Foreground(Ink).Background(Accent).Bold(true).Render(label)
		default:
			parts[i] = Fainter.Render(label)
		}
	}
	return strings.Join(parts, Fainter.Render("──"))
}

// Bar is a progress bar for a bake period.
func Bar(fraction float64, width int) string {
	fraction = max(0, min(fraction, 1))
	full := int(fraction * float64(width))
	return Green.Render(strings.Repeat("━", full)) + lipgloss.NewStyle().Foreground(lipgloss.Color("#26262B")).Render(strings.Repeat("━", width-full))
}

// OK, WarnLine and Fail are one-line results with icons.
func OK(msg string) string       { return Green.Render("✓ ") + msg }
func WarnLine(msg string) string { return Amber.Render("! ") + msg }
func Fail(msg string) string     { return Red.Render("✗ ") + msg }

// Hint suggests a next command.
func Hint(cmd, why string) string {
	return Fainter.Render("→ ") + Code.Render(cmd) + Fainter.Render("  "+why)
}

// Table renders rows with the house style.
func Table(headers []string, rows [][]string) string {
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(Line)).
		BorderColumn(false).
		BorderLeft(false).BorderRight(false).BorderTop(false).BorderBottom(false).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().PaddingRight(3)
			if row == table.HeaderRow {
				return s.Foreground(Faint)
			}
			return s.Foreground(Fg)
		})
	return t.Render()
}

// Receipt renders the paper release receipt shown at the end of `alror deploy`.
// Every line is one full-width run with the paper background: nesting styled
// strings would reset the background mid-line and stripe the paper.
func Receipt(d *domain.Deployment, v *domain.Verdict, elapsed time.Duration) string {
	const width = 44
	ink := lipgloss.NewStyle().Foreground(Ink).Background(Paper)
	bold := ink.Bold(true)
	grey := lipgloss.NewStyle().Foreground(lipgloss.Color("#6E6A60")).Background(Paper)

	line := func(st lipgloss.Style, l, r string) string {
		gap := max(1, width-lipgloss.Width(l)-lipgloss.Width(r))
		if r == "" {
			gap = width - lipgloss.Width(l)
		}
		return st.Render("  " + l + strings.Repeat(" ", max(0, gap)) + r + "  ")
	}
	rule := line(grey, strings.TrimSpace(strings.Repeat("- ", width/2)), "")
	blank := line(ink, "", "")

	headline, outLabel, outValue := "VERIFIED", "Promoted", "→ 100%"
	switch d.Status {
	case domain.StatusRolledBack:
		headline, outLabel, outValue = "ROLLED BACK", "Reverted at", fmt.Sprintf("%d%%", d.Weight)
	case domain.StatusFailed:
		headline, outLabel, outValue = "FAILED", "Stopped at", fmt.Sprintf("%d%%", d.Weight)
	}

	lines := []string{
		blank,
		line(grey, "ALROR · RELEASE RECEIPT", ""),
		blank,
		line(bold, headline, ""),
		line(grey, fmt.Sprintf("%s %s · risk %d", d.Service, d.Ref, d.Risk.Score), ""),
		rule,
	}
	if v != nil {
		for _, r := range v.Results {
			lines = append(lines, line(ink, r.Metric, fmt.Sprintf("%.3g / %.3g", r.Canary, r.Baseline)))
		}
		lines = append(lines, rule)
	}
	lines = append(lines,
		line(bold, outLabel, outValue),
		line(grey, d.ID, elapsed.Round(time.Millisecond).String()),
		blank,
		lipgloss.NewStyle().Foreground(Paper).Render(strings.Repeat("▼", width+4)),
	)
	return strings.Join(lines, "\n")
}
