package agenthud

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"tmux-menu/internal/agentstatus"
)

type Color uint8

const (
	ColorDefault Color = iota
	ColorBlack
	ColorRed
	ColorGreen
	ColorYellow
	ColorBlue
	ColorMagenta
	ColorCyan
	ColorWhite
	ColorBrightBlack
	ColorBrightRed
	ColorBrightGreen
	ColorBrightYellow
	ColorBrightBlue
	ColorBrightMagenta
	ColorBrightCyan
	ColorBrightWhite
	ColorOrange
	ColorDim
)

type ThemeConfig struct {
	CodexIcon, ClaudeIcon, OtherIcon, CurrentIcon                            string
	AttentionIcon, WorkingIcon, CompletedIcon, WaitingIcon, UnknownIcon      string
	CodexColor, ClaudeColor, OtherColor, ThreadColor, WorkdirColor           string
	AttentionColor, WorkingColor, CompletedColor, WaitingColor, UnknownColor string
}

type Theme struct {
	initialized                                                              bool
	codex, claude, other, current                                            Text
	attention, working, completed, waiting, unknown                          Text
	codexColor, claudeColor, otherColor                                      Color
	threadColor, workdirColor                                                Color
	attentionColor, workingColor, completedColor, waitingColor, unknownColor Color
}

func DefaultTheme() Theme {
	theme, _ := NewTheme(ThemeConfig{
		CodexIcon: ">", ClaudeIcon: "✳", CurrentIcon: "*",
		AttentionIcon: "!", WorkingIcon: "●", CompletedIcon: "✓", WaitingIcon: "○", UnknownIcon: "?",
		CodexColor: "blue", ClaudeColor: "orange", OtherColor: "magenta", ThreadColor: "default", WorkdirColor: "dim",
		AttentionColor: "red", WorkingColor: "green", CompletedColor: "bright_cyan", WaitingColor: "yellow", UnknownColor: "dim",
	})
	return theme
}

func NewTheme(config ThemeConfig) (Theme, error) {
	values := []string{
		config.CodexColor, config.ClaudeColor, config.OtherColor, config.ThreadColor, config.WorkdirColor,
		config.AttentionColor, config.WorkingColor, config.CompletedColor, config.WaitingColor, config.UnknownColor,
	}
	parsed := make([]Color, len(values))
	for index, value := range values {
		color, err := ParseColor(value)
		if err != nil {
			return Theme{}, err
		}
		parsed[index] = color
	}
	return Theme{
		initialized: true,
		codex:       SanitizeText(config.CodexIcon, 8), claude: SanitizeText(config.ClaudeIcon, 8), other: SanitizeText(config.OtherIcon, 8), current: SanitizeText(config.CurrentIcon, 8),
		attention: SanitizeText(config.AttentionIcon, 8), working: SanitizeText(config.WorkingIcon, 8), completed: SanitizeText(config.CompletedIcon, 8), waiting: SanitizeText(config.WaitingIcon, 8), unknown: SanitizeText(config.UnknownIcon, 8),
		codexColor: parsed[0], claudeColor: parsed[1], otherColor: parsed[2], threadColor: parsed[3], workdirColor: parsed[4],
		attentionColor: parsed[5], workingColor: parsed[6], completedColor: parsed[7], waitingColor: parsed[8], unknownColor: parsed[9],
	}, nil
}

func ParseColor(value string) (Color, error) {
	colors := map[string]Color{
		"": ColorDefault, "default": ColorDefault, "black": ColorBlack, "red": ColorRed, "green": ColorGreen,
		"yellow": ColorYellow, "blue": ColorBlue, "magenta": ColorMagenta, "cyan": ColorCyan, "white": ColorWhite,
		"bright_black": ColorBrightBlack, "bright_red": ColorBrightRed, "bright_green": ColorBrightGreen,
		"bright_yellow": ColorBrightYellow, "bright_blue": ColorBrightBlue, "bright_magenta": ColorBrightMagenta,
		"bright_cyan": ColorBrightCyan, "bright_white": ColorBrightWhite, "orange": ColorOrange, "dim": ColorDim,
	}
	color, ok := colors[value]
	if !ok {
		return ColorDefault, fmt.Errorf("unsupported HUD color %q", value)
	}
	return color, nil
}

func (t Theme) status(status agentstatus.State) Text {
	icon, color, word := t.statusParts(status)
	return joinText(styled(icon, color, status == agentstatus.StateAttention), SanitizeText(" "+word, 16))
}

func (t Theme) statusParts(status agentstatus.State) (Text, Color, string) {
	icon, color, word := t.unknown, t.unknownColor, "unknown"
	switch status {
	case agentstatus.StateAttention:
		icon, color, word = t.attention, t.attentionColor, "attention"
	case agentstatus.StateWorking:
		icon, color, word = t.working, t.workingColor, "working"
	case agentstatus.StateCompleted:
		icon, color, word = t.completed, t.completedColor, "completed"
	case agentstatus.StateWaiting:
		icon, color, word = t.waiting, t.waitingColor, "waiting"
	}
	if icon.RetainedBytes() == 0 {
		icon = SanitizeText(statusMarker(status), 8)
	}
	return icon, color, word

}

func (t Theme) summaryStatus(status agentstatus.State, count int, words bool) Text {
	icon, color, word := t.statusParts(status)
	result := styled(icon, color, status == agentstatus.StateAttention)
	if words {
		result = joinText(result, SanitizeText(" "+word, 16))
	}
	return joinText(result, SanitizeText(fmt.Sprintf(" %d", count), 24))
}

func (t Theme) product(agent Agent) Text {
	icon, color := t.other, t.otherColor
	switch agent.providerKind {
	case agentstatus.ProviderCodex:
		icon, color = t.codex, t.codexColor
	case agentstatus.ProviderClaude:
		icon, color = t.claude, t.claudeColor
	}
	if icon.RetainedBytes() == 0 {
		return styled(agent.provider, color, false)
	}
	return joinText(styled(icon, color, false), SanitizeText(" ", 1), agent.provider)
}

func styled(value Text, color Color, bold bool) Text {
	result := Text{width: value.width, bytes: value.bytes, spans: make([]span, len(value.spans))}
	for index, part := range value.spans {
		part.style.bold = part.style.bold || bold
		part.style.foreground = 0
		part.style.foreground256 = 0
		if color == ColorDim {
			part.style.dim = true
		} else if color == ColorOrange {
			part.style.foreground256 = 208
		} else if code := colorANSI(color); code != 0 {
			part.style.foreground = code
		}
		result.spans[index] = part
	}
	return result
}

func colorANSI(color Color) int {
	switch color {
	case ColorBlack:
		return 30
	case ColorRed:
		return 31
	case ColorGreen:
		return 32
	case ColorYellow:
		return 33
	case ColorBlue:
		return 34
	case ColorMagenta:
		return 35
	case ColorCyan:
		return 36
	case ColorWhite:
		return 37
	case ColorBrightBlack:
		return 90
	case ColorBrightRed:
		return 91
	case ColorBrightGreen:
		return 92
	case ColorBrightYellow:
		return 93
	case ColorBrightBlue:
		return 94
	case ColorBrightMagenta:
		return 95
	case ColorBrightCyan:
		return 96
	case ColorBrightWhite:
		return 97
	default:
		return 0
	}
}

func Render(model Model) string {
	layout := model.currentLayout()
	lines := make([]string, 0, layout.Height)
	if layout.HeaderRows > 0 {
		lines = append(lines, renderSummary(model, layout.Width))
	}
	if layout.BodyHeight > 0 {
		lines = append(lines, renderBody(model, layout)...)
	}
	lines = append(lines, renderFooter(model, layout.Width, layout.FooterRows)...)
	if len(lines) > layout.Height {
		lines = lines[:layout.Height]
	}
	for len(lines) < layout.Height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func renderSummary(model Model, width int) string {
	summary := model.Summary()
	words := width >= hudWideWidth
	parts := []Text{SanitizeText(fmt.Sprintf("Agents %d | ", summary.Total), width)}
	if model.refreshFailure.RetainedBytes() > 0 {
		parts = append(parts, SanitizeText("stale ", width), model.refreshFailure, SanitizeText(" | ", width))
	}
	statuses := []struct {
		state agentstatus.State
		count int
	}{
		{agentstatus.StateAttention, summary.Attention},
		{agentstatus.StateWorking, summary.Working},
		{agentstatus.StateCompleted, summary.Completed},
		{agentstatus.StateWaiting, summary.Waiting},
		{agentstatus.StateUnknown, summary.Unknown},
	}
	for index, status := range statuses {
		if index > 0 {
			parts = append(parts, SanitizeText(" | ", width))
		}
		parts = append(parts, model.theme.summaryStatus(status.state, status.count, words))
	}
	page := fmt.Sprintf(" | %d/%d", model.Page()+1, model.PageCount())
	if words {
		page = fmt.Sprintf(" | page %d/%d", model.Page()+1, model.PageCount())
	}
	parts = append(parts, SanitizeText(page, width))
	return renderSafeLine(joinText(parts...), width)
}

func renderBody(model Model, layout Layout) []string {
	if len(model.slots) == 0 {
		return renderWaiting(layout.Width, layout.BodyHeight)
	}
	visible := model.visiblePaneIDs()
	result := make([]string, 0, layout.BodyHeight)
	cellIndex := 0
	for row := 0; row < layout.Rows; row++ {
		rowCells := make([][]string, 0, layout.Columns)
		rowHeight := 1
		for column := 0; column < layout.Columns; column++ {
			cell := layout.Cells[cellIndex]
			rowHeight = cell.Height
			if cellIndex < len(visible) {
				paneID := visible[cellIndex]
				rowCells = append(rowCells, renderCell(model, model.agents[paneID], model.tails[paneID], cell, paneID == model.selected))
			} else {
				rowCells = append(rowCells, blankCell(cell.Width, cell.Height))
			}
			cellIndex++
		}
		for lineIndex := 0; lineIndex < rowHeight; lineIndex++ {
			var line strings.Builder
			for column, cell := range rowCells {
				if column > 0 {
					line.WriteByte(' ')
				}
				line.WriteString(cell[lineIndex])
			}
			result = append(result, clipANSI(line.String(), layout.Width))
		}
		if row+1 < layout.Rows {
			result = append(result, "")
		}
	}
	for len(result) < layout.BodyHeight {
		result = append(result, "")
	}
	if len(result) > layout.BodyHeight {
		result = result[:layout.BodyHeight]
	}
	return result
}

func renderWaiting(width, height int) []string {
	values := []string{
		"Waiting for live agents…",
		"The HUD keeps discovering panes; no relaunch is needed.",
		"/ picker   agents --picker direct   q quit",
	}
	result := make([]string, 0, height)
	padding := max((height-len(values))/2, 0)
	for range padding {
		result = append(result, "")
	}
	for _, value := range values {
		if len(result) >= height {
			break
		}
		result = append(result, renderSafeLine(SanitizeText(value, width), width))
	}
	for len(result) < height {
		result = append(result, "")
	}
	return result
}

func renderCell(model Model, agent Agent, tail tailState, cell Rect, selected bool) []string {
	width, height := max(cell.Width, 1), max(cell.Height, 1)
	if width < 4 || height < 3 {
		marker := statusMarker(agent.status)
		if selected {
			marker = ">" + marker
		}
		return fillLines([]string{renderSafeLine(SanitizeText(marker, width), width)}, width, height)
	}
	inner := width - 2
	top := "┌" + strings.Repeat("─", inner) + "┐"
	bottom := "└" + strings.Repeat("─", inner) + "┘"
	thread := styled(agent.thread, model.theme.threadColor, true)
	if agent.currentThread {
		thread = joinText(styled(model.theme.current, model.theme.threadColor, true), thread)
	}
	header := joinText(
		SanitizeText(selectedPrefix(selected), inner),
		model.theme.status(agent.status),
		SanitizeText(" ", inner),
		model.theme.product(agent),
		SanitizeText(" ", inner),
		styled(agent.session, agent.sessionColor, true),
		SanitizeText(" ", inner),
		thread,
	)
	metadata := agentMetadata(model.now, agent, model.theme)
	if tail.stale {
		metadata = joinText(metadata, SanitizeText(" [stale", inner))
		if tail.failure.RetainedBytes() > 0 {
			metadata = joinText(metadata, SanitizeText(": ", inner), tail.failure)
		}
		metadata = joinText(metadata, SanitizeText("]", inner))
	}

	lines := []string{top, framed(renderSafeLine(header, inner), inner), framed(renderSafeLine(metadata, inner), inner)}
	terminalRows := max(height-len(lines)-1, 0)
	terminalLines := tail.terminal.lines
	if len(terminalLines) > terminalRows {
		terminalLines = terminalLines[len(terminalLines)-terminalRows:]
	}
	for range terminalRows - len(terminalLines) {
		lines = append(lines, framed("", inner))
	}
	for _, terminalLine := range terminalLines {
		lines = append(lines, framed(renderSafeLine(terminalLine, inner), inner))
	}
	lines = append(lines, bottom)
	return fillLines(lines, width, height)
}

func blankCell(width, height int) []string {
	return fillLines(nil, width, height)
}

func fillLines(lines []string, width, height int) []string {
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

func framed(value string, width int) string {
	return "│" + padANSI(value, width) + "│"
}

func selectedPrefix(selected bool) string {
	if selected {
		return "> "
	}
	return "  "
}

func statusMarker(status agentstatus.State) string {
	switch status {
	case agentstatus.StateAttention:
		return "! attention"
	case agentstatus.StateWorking:
		return "▶ working"
	case agentstatus.StateCompleted:
		return "✓ completed"
	case agentstatus.StateWaiting:
		return "… waiting"
	default:
		return "? unknown"
	}
}

func agentMetadata(now time.Time, agent Agent, theme Theme) Text {
	parts := []Text{styled(agent.workdir, theme.workdirColor, false)}
	if agent.children > 0 {
		parts = append(parts, SanitizeText(fmt.Sprintf(" children %d", agent.children), 32))
	}
	if !now.IsZero() {
		if !agent.turnStartedAt.IsZero() {
			parts = append(parts, SanitizeText(" turn "+compactDuration(now.Sub(agent.turnStartedAt)), 32))
		}
		if !agent.stateChangedAt.IsZero() {
			parts = append(parts, SanitizeText(" state "+compactDuration(now.Sub(agent.stateChangedAt)), 32))
		}
		if !agent.lastEventAt.IsZero() {
			parts = append(parts, SanitizeText(" event "+compactDuration(now.Sub(agent.lastEventAt)), 32))
		}
	}
	return joinText(parts...)
}

func compactDuration(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	switch {
	case value < time.Minute:
		return fmt.Sprintf("%ds", int(value/time.Second))
	case value < time.Hour:
		return fmt.Sprintf("%dm", int(value/time.Minute))
	default:
		return fmt.Sprintf("%dh", int(value/time.Hour))
	}
}

func renderFooter(model Model, width, rows int) []string {
	if rows <= 0 {
		return nil
	}
	values := []string{"arrows/hjkl move  [/] page  n attention  z focus  / picker  enter switch  ? help  q quit"}
	if model.fullHelp && rows >= 3 {
		values = []string{
			"arrows/hjkl move   [/] page   n next attention",
			"z focus   / picker",
			"enter switch   ? help   q quit",
		}
	}
	result := make([]string, 0, rows)
	for _, value := range values {
		result = append(result, renderSafeLine(SanitizeText(value, width), width))
	}
	for len(result) < rows {
		result = append(result, "")
	}
	return result[:rows]
}

func joinText(values ...Text) Text {
	var result Text
	for _, value := range values {
		for _, part := range value.spans {
			appendParsed(&result.spans, string(part.text), part.style)
		}
		result.width += value.width
		result.bytes += value.bytes
	}
	return result
}

func renderSafeLine(value Text, width int) string {
	clipped := clipLine(value.spans, max(width, 0), defaultTextBytes)
	return clipped.ANSI()
}

func padANSI(value string, width int) string {
	return value + strings.Repeat(" ", max(width-ansi.StringWidth(value), 0))
}

func clipANSI(value string, width int) string {
	return ansi.Truncate(value, max(width, 0), "")
}
