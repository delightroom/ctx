package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	sessionpreview "github.com/delightroom/ctx/internal/preview"
)

const (
	minWidth      = 60
	minHeight     = 18
	wideThreshold = 100
)

func (m *Model) render() string {
	if m.width < minWidth || m.height < minHeight {
		message := fmt.Sprintf(
			"ctx needs at least %dx%d\ncurrent terminal: %dx%d",
			minWidth, minHeight, m.width, m.height,
		)
		return lipgloss.Place(
			max(1, m.width), max(1, m.height),
			lipgloss.Center, lipgloss.Center,
			m.styles.warning.Render(message),
		)
	}
	if m.showIntro {
		return m.renderIntro()
	}

	header := m.renderHeader()
	footer := m.renderFooter()
	contentHeight := max(1, m.height-lipgloss.Height(header)-lipgloss.Height(footer))
	detailHeight := max(10, contentHeight/2)
	listsHeight := contentHeight - detailHeight

	var lists string
	if m.width >= wideThreshold {
		leftWidth := m.width / 2
		rightWidth := m.width - leftWidth
		lists = lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.renderLocalPanel(leftWidth, listsHeight),
			m.renderSharedPanel(rightWidth, listsHeight),
		)
	} else {
		localHeight := listsHeight / 2
		sharedHeight := listsHeight - localHeight
		lists = lipgloss.JoinVertical(
			lipgloss.Left,
			m.renderLocalPanel(m.width, localHeight),
			m.renderSharedPanel(m.width, sharedHeight),
		)
	}

	detail := m.renderDetailPanel(m.width, detailHeight)
	body := lipgloss.JoinVertical(lipgloss.Left, header, lists, detail)
	body = lipgloss.NewStyle().
		Width(m.width).
		Height(max(1, m.height-lipgloss.Height(footer))).
		Render(body)
	base := lipgloss.JoinVertical(lipgloss.Left, body, footer)
	base = lipgloss.NewStyle().Width(m.width).Height(m.height).Render(base)

	if m.showHelp {
		return m.overlay(base, m.renderHelpModal())
	}
	if m.showActions {
		return m.overlay(base, m.renderActionModal())
	}
	if m.showTranscript {
		return m.overlay(base, m.renderTranscriptModal())
	}
	return base
}

func (m *Model) renderHeader() string {
	tailnet := m.styles.warning.Render("TAILNET CHECKING")
	if !m.statusLoading {
		switch {
		case m.status.TailnetReady:
			tailnet = m.styles.success.Render("TAILNET " + displayValue(m.status.TailnetName, "ONLINE"))
		case m.statusError != "":
			tailnet = m.styles.err.Render("TAILNET ERROR")
		default:
			tailnet = m.styles.err.Render("TAILNET OFFLINE")
		}
	}

	agents := "AGENTS NONE"
	if len(m.status.Agents) > 0 {
		agents = "AGENTS " + strings.Join(m.status.Agents, " + ")
	}
	line := fmt.Sprintf("ctx %s  │  %s  │  %s",
		displayValue(m.version, "dev"),
		tailnet,
		m.styles.muted.Render(agents),
	)
	return m.styles.header.Width(m.width).Render(cellTruncate(line, m.width-2))
}

func (m *Model) renderFooter() string {
	help := "tab focus  ↑↓ move  v inspect  enter actions  / filter  r refresh  ? help  q quit"
	if m.width < 78 {
		help = "tab focus  ↑↓ move  v inspect  enter menu  ? help  q quit"
	}
	if m.filtering {
		help = "type to filter  enter apply  esc cancel"
	} else if m.showActions {
		help = "↑↓/jk choose  enter run  esc cancel  q quit"
	} else if m.showHelp {
		help = "esc/? close help  q quit"
	}
	return m.styles.footer.Width(m.width).Render(cellTruncate(help, m.width))
}

func (m *Model) renderLocalPanel(width, height int) string {
	scope := "WORKSPACE"
	if m.allLocal {
		scope = "ALL"
	}
	items := m.filteredLocal()
	title := fmt.Sprintf("LOCAL SESSIONS  %s  %d", scope, len(items))
	titleMarker := "  "
	if m.focus == localPanel {
		titleMarker = "> "
	}
	lines := []string{m.styles.title.Render(titleMarker + title)}
	if m.filtering && m.focus == localPanel {
		lines = append(lines, m.filterInput.View())
	} else if m.localFilter != "" {
		lines = append(lines, m.styles.muted.Render("/ "+m.localFilter))
	}

	rows := panelRows(height, len(lines))
	switch {
	case m.localLoading:
		lines = append(lines, m.spinner.View()+" Discovering Claude and Codex sessions...")
	case m.localError != "":
		lines = append(lines, m.styles.err.Render(cellTruncate(m.localError, width-6)))
	case len(items) == 0:
		message := "No sessions found for this workspace."
		if m.allLocal {
			message = "No Claude or Codex sessions found."
		} else if m.localFilter != "" {
			message = "No local sessions match the filter."
		}
		lines = append(lines, m.styles.muted.Render(message))
	default:
		start := visibleStart(m.localIndex, rows, len(items))
		for index := start; index < min(len(items), start+rows); index++ {
			session := items[index]
			row := fmt.Sprintf("%-7s %-24s %s",
				displayAgent(session.SourceAgent),
				displayValue(session.Project, "-"),
				relativeTime(session.ModifiedAt),
			)
			lines = append(lines, m.renderRow(row, width-4, m.focus == localPanel && index == m.localIndex))
		}
	}
	return m.renderPanel(strings.Join(lines, "\n"), width, height, m.focus == localPanel)
}

func (m *Model) renderSharedPanel(width, height int) string {
	items := m.filteredShared()
	title := fmt.Sprintf("SHARED CONTEXTS  %d", len(items))
	titleMarker := "  "
	if m.focus == sharedPanel {
		titleMarker = "> "
	}
	lines := []string{m.styles.title.Render(titleMarker + title)}
	if m.filtering && m.focus == sharedPanel {
		lines = append(lines, m.filterInput.View())
	} else if m.sharedFilter != "" {
		lines = append(lines, m.styles.muted.Render("/ "+m.sharedFilter))
	}
	rows := panelRows(height, len(lines))
	switch {
	case m.sharedLoading:
		lines = append(lines, m.spinner.View()+" Discovering tailnet contexts...")
	case m.sharedError != "":
		lines = append(lines, m.styles.err.Render(cellTruncate(m.sharedError, width-6)))
	case len(items) == 0:
		message := "No shared contexts are reachable."
		if m.sharedFilter != "" {
			message = "No shared contexts match the filter."
		}
		lines = append(lines, m.styles.muted.Render(message))
	default:
		start := visibleStart(m.sharedIndex, rows, len(items))
		for index := start; index < min(len(items), start+rows); index++ {
			shared := items[index]
			row := fmt.Sprintf("%-26s %-7s %s",
				displayValue(shared.Node, "-")+"/"+displayValue(shared.Name, "-"),
				displayAgent(shared.SourceAgent),
				relativeTime(shared.UpdatedAt),
			)
			lines = append(lines, m.renderRow(row, width-4, m.focus == sharedPanel && index == m.sharedIndex))
		}
	}
	return m.renderPanel(strings.Join(lines, "\n"), width, height, m.focus == sharedPanel)
}

func (m *Model) renderDetailPanel(width, height int) string {
	scope := "LOCAL"
	if m.focus == sharedPanel {
		scope = "SHARED"
	}
	lines := []string{m.styles.title.Render("SELECTION  " + scope)}
	if m.notice != "" {
		lines = append(lines, m.styles.warning.Render(cellTruncate(m.notice, width-6)))
	}

	if m.focus == localPanel {
		session, ok := m.selectedLocal()
		if !ok {
			lines = append(lines, m.styles.muted.Render("Select a local session to inspect and host."))
		} else {
			lines = append(lines,
				fmt.Sprintf("%s · %s · %s · session %s",
					displayAgent(session.SourceAgent),
					displayValue(session.Project, "-"),
					relativeTime(session.ModifiedAt),
					displayValue(session.SessionID, "-")),
			)
			if m.status.TailnetReady {
				lines = append(lines, m.styles.success.Render("Enter / h  Host this session"))
			} else {
				reason := displayValue(m.status.TailnetError, "connect Tailscale first")
				lines = append(lines, m.styles.warning.Render("Host unavailable: "+reason))
			}
			lines = append(lines, m.renderPreviewLines()...)
			lines = append(lines,
				detailLine(m, "Workspace", displayValue(session.CWD, "-")),
				detailLine(m, "Source", displayValue(session.Path, "-")),
			)
		}
	} else {
		shared, ok := m.selectedShared()
		if !ok {
			lines = append(lines, m.styles.muted.Render("Select a shared context to tail or continue."))
		} else {
			lines = append(lines,
				fmt.Sprintf("%s · %s · %s · %s",
					displayLocator(shared),
					displayAgent(shared.SourceAgent),
					displayValue(shared.Project, "-"),
					relativeTime(shared.UpdatedAt)),
				fmt.Sprintf("Owner %s · revision %s",
					displayValue(shared.Owner, "-"),
					displayValue(shared.Revision, "-")),
				m.styles.success.Render("Enter actions · t tail · f follow · c continue"),
			)
			lines = append(lines, m.renderPreviewLines()...)
		}
	}

	content := fitContent(lines, width-4, max(1, height-2))
	return m.renderPanel(content, width, height, false)
}

func (m *Model) renderPreviewLines(metadata ...string) []string {
	lines := []string{m.styles.title.Render("SESSION PEEK")}
	switch {
	case m.previewLoading:
		lines = append(lines, m.spinner.View()+" Reading the selected session...")
		return append(lines, metadata...)
	case m.previewError != "":
		lines = append(lines, m.styles.warning.Render("Preview unavailable: "+m.previewError))
		return append(lines, metadata...)
	case m.previewKey == "":
		lines = append(lines, m.styles.muted.Render("Move onto a session to load its preview."))
		return append(lines, metadata...)
	}

	if m.preview.CurrentRequest != "" {
		lines = append(lines, detailLine(m, "Current", m.preview.CurrentRequest))
	} else {
		lines = append(lines, detailLine(m, "Current", "No user message in the visible digest."))
	}
	lines = append(lines, detailLine(m, "Activity", activitySummary(m.preview)))
	if m.preview.TranscriptCount > 0 {
		lines = append(lines, detailLine(
			m,
			"History",
			fmt.Sprintf(
				"%d entries · page %d/%d · v inspect",
				m.preview.TranscriptCount,
				m.preview.TranscriptPage,
				m.preview.TranscriptPages,
			),
		))
	}
	lines = append(lines, metadata...)
	if len(m.preview.Recent) > 0 {
		lines = append(lines, m.styles.title.Render("RECENT TURNS"))
		for _, turn := range m.preview.Recent {
			lines = append(lines, detailLine(m, turn.Role, turn.Text))
		}
	}
	return lines
}

func detailLine(m *Model, label, value string) string {
	return m.styles.muted.Render(fmt.Sprintf("%-10s", label)) + value
}

func activitySummary(summary sessionpreview.Summary) string {
	activity := fmt.Sprintf(
		"%d events · %d you · %d agent · %d tool calls",
		summary.EventCount,
		summary.UserTurns,
		summary.AgentTurns,
		summary.ToolCalls,
	)
	if len(summary.Tools) > 0 {
		limit := min(4, len(summary.Tools))
		activity += " · " + strings.Join(summary.Tools[:limit], ", ")
		if len(summary.Tools) > limit {
			activity += fmt.Sprintf(" +%d", len(summary.Tools)-limit)
		}
	}
	if summary.Truncated {
		activity += " · digest clipped"
	}
	return activity
}

func (m *Model) renderPanel(content string, width, height int, active bool) string {
	style := m.styles.panel
	if active {
		style = m.styles.panelActive
	}
	return style.Width(max(1, width)).Height(max(1, height)).Render(content)
}

func (m *Model) renderRow(value string, width int, selected bool) string {
	marker := "  "
	if selected {
		marker = "> "
	}
	contentWidth := max(1, width-lipgloss.Width(marker))
	value = marker + padRight(cellTruncate(value, contentWidth), contentWidth)
	if selected {
		return m.styles.rowSelected.Render(value)
	}
	return m.styles.row.Render(value)
}

func (m *Model) renderActionModal() string {
	options := m.actionOptions()
	lines := []string{m.styles.title.Render("AVAILABLE ACTIONS"), ""}
	for index, option := range options {
		label := option.label
		if !option.enabled {
			label += "  unavailable"
		}
		marker := "  "
		if index == m.actionIndex {
			marker = "> "
		}
		labelWidth := 48 - lipgloss.Width(marker)
		label = marker + padRight(cellTruncate(label, labelWidth), labelWidth)
		if index == m.actionIndex {
			lines = append(lines, m.styles.modalSelected.Render(label))
		} else if option.enabled {
			lines = append(lines, label)
		} else {
			lines = append(lines, m.styles.muted.Render(label))
		}
	}
	lines = append(lines, "", m.styles.muted.Render("enter run · esc cancel"))
	return m.styles.modal.Width(50).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderTranscriptModal() string {
	width := min(104, max(56, m.width-4))
	height := min(34, max(12, m.height-4))
	innerWidth := max(1, width-8)
	innerHeight := max(1, height-2)

	contextLabel := "No session selected"
	if m.focus == localPanel {
		if session, ok := m.selectedLocal(); ok {
			contextLabel = fmt.Sprintf(
				"LOCAL · %s · %s",
				displayAgent(session.SourceAgent),
				displayValue(session.Project, "-"),
			)
		}
	} else if shared, ok := m.selectedShared(); ok {
		contextLabel = fmt.Sprintf(
			"SHARED · %s · %s",
			displayLocator(shared),
			displayAgent(shared.SourceAgent),
		)
	}

	pageLabel := "NO DISPLAYABLE ENTRIES"
	if m.preview.TranscriptPages > 0 {
		position := ""
		switch m.preview.TranscriptPage {
		case 1:
			position = " · NEWEST"
		case m.preview.TranscriptPages:
			position = " · OLDEST"
		}
		pageLabel = fmt.Sprintf(
			"PAGE %d / %d%s · %d ENTRIES",
			m.preview.TranscriptPage,
			m.preview.TranscriptPages,
			position,
			m.preview.TranscriptCount,
		)
	}

	lines := []string{
		m.styles.title.Render("SESSION TRANSCRIPT"),
		cellTruncate(contextLabel+"  ·  "+pageLabel, innerWidth),
		"",
	}
	switch {
	case m.previewLoading:
		lines = append(lines, m.spinner.View()+" Loading transcript page...")
	case m.previewError != "":
		lines = append(lines, m.styles.warning.Render("Preview unavailable: "+m.previewError))
	case len(m.preview.Entries) == 0:
		lines = append(lines, m.styles.muted.Render("No displayable messages or tool activity."))
	default:
		entryBudget := max(1, innerHeight-5)
		linesPerEntry := min(3, max(1, entryBudget/len(m.preview.Entries)))
		for _, entry := range m.preview.Entries {
			lines = append(lines, renderTranscriptEntry(entry, innerWidth, linesPerEntry)...)
		}
	}
	lines = append(
		lines,
		"",
		m.styles.muted.Render("[ / PgUp older · ] / PgDn newer · Home/End jump · Esc close"),
	)
	content := fitContent(lines, innerWidth, innerHeight)
	return m.styles.modal.Width(width).Height(height).Render(content)
}

func renderTranscriptEntry(entry sessionpreview.Entry, width, maxLines int) []string {
	timestamp := "     "
	if !entry.Timestamp.IsZero() {
		timestamp = entry.Timestamp.Local().Format("15:04")
	}
	prefix := fmt.Sprintf("%5s  %-7s ", timestamp, entry.Role)
	textWidth := max(1, width-lipgloss.Width(prefix))
	wrapped := strings.Split(ansi.Wrap(entry.Text, textWidth, " /"), "\n")
	if len(wrapped) > maxLines {
		wrapped = wrapped[:maxLines]
		wrapped[len(wrapped)-1] = ansi.Truncate(wrapped[len(wrapped)-1]+"…", textWidth, "…")
	}
	lines := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		if index == 0 {
			lines = append(lines, prefix+line)
		} else {
			lines = append(lines, strings.Repeat(" ", lipgloss.Width(prefix))+line)
		}
	}
	return lines
}

func (m *Model) renderHelpModal() string {
	width := min(68, max(44, m.width-4))
	var lines []string
	if m.width < 76 || m.height < 22 {
		lines = []string{
			m.styles.title.Render("CTX KEYBOARD REFERENCE"),
			"",
			"Tab / Shift+Tab  focus    ↑↓ / jk       move",
			"PgUp / PgDown   jump     /             filter",
			"a               scope    r             refresh",
			"v               inspect  [ / ]         transcript pages",
			"Enter           actions  h             host",
			"t               tail     f             follow",
			"c               continue ? / Esc       close",
			"q / Ctrl+C      quit",
		}
	} else {
		lines = []string{
			m.styles.title.Render("CTX KEYBOARD REFERENCE"),
			"",
			"Tab / Shift+Tab     Change focused panel",
			"↑ / ↓ or j / k      Move selection",
			"PageUp / PageDown   Move five rows",
			"/                   Filter the focused panel",
			"a                   Toggle workspace/all local sessions",
			"r                   Refresh status and inventories",
			"v                   Open paged session transcript",
			"Enter               Open available actions",
			"h                   Host selected local session",
			"t / f               Tail once / follow shared context",
			"c                   Continue selected shared context",
			"?                   Open or close this help",
			"q / Ctrl+C          Quit",
			"",
			m.styles.muted.Render("Explicit CLI commands remain available outside the TUI."),
		}
	}
	return m.styles.modal.Width(width).Render(strings.Join(lines, "\n"))
}

func (m *Model) overlay(base, modal string) string {
	canvas := lipgloss.NewCanvas(m.width, m.height)
	canvas.Compose(lipgloss.NewLayer(base))
	x := max(0, (m.width-lipgloss.Width(modal))/2)
	y := max(0, (m.height-lipgloss.Height(modal))/2)
	canvas.Compose(lipgloss.NewLayer(modal).X(x).Y(y).Z(1))
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Render(canvas.Render())
}

func panelRows(height, reserved int) int {
	return max(1, height-2-reserved)
}

func visibleStart(selected, visible, total int) int {
	if visible <= 0 || total <= visible {
		return 0
	}
	start := selected - visible + 1
	if start < 0 {
		start = 0
	}
	if start+visible > total {
		start = total - visible
	}
	return start
}

func fitContent(lines []string, width, height int) string {
	result := make([]string, 0, min(len(lines), height))
	for _, line := range lines {
		if len(result) == height {
			break
		}
		result = append(result, cellTruncate(line, width))
	}
	return strings.Join(result, "\n")
}

func cellTruncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}

func padRight(value string, width int) string {
	missing := width - lipgloss.Width(value)
	if missing <= 0 {
		return value
	}
	return value + strings.Repeat(" ", missing)
}

func displayAgent(value string) string {
	switch value {
	case "claude-code":
		return "Claude"
	case "codex-cli":
		return "Codex"
	default:
		return displayValue(value, "-")
	}
}

func displayValue(value, fallback string) string {
	value = sessionpreview.Sanitize(value)
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func displayLocator(shared SharedContext) string {
	return fmt.Sprintf(
		"ctx://%s/%s",
		displayValue(shared.Node, "-"),
		displayValue(shared.Name, "-"),
	)
}

func relativeTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	delta := time.Since(value)
	if delta < time.Minute {
		return fmt.Sprintf("%ds ago", max(0, int(delta.Seconds())))
	}
	if delta < time.Hour {
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	}
	return value.Local().Format("2006-01-02 15:04")
}
