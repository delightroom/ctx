package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/delightroom/ctx/internal/preview"
)

func TestRenderWideAndStackedLayouts(t *testing.T) {
	model := readyModel()
	model.width = 120
	model.height = 36
	wide := model.render()
	if lipgloss.Width(wide) != 120 || lipgloss.Height(wide) != 36 {
		t.Fatalf("wide dimensions = %dx%d", lipgloss.Width(wide), lipgloss.Height(wide))
	}
	if !sameLine(wide, "LOCAL SESSIONS", "SHARED CONTEXTS") {
		t.Fatalf("wide layout did not render panels side by side:\n%s", stripANSI(wide))
	}
	plainWide := stripANSI(wide)
	if !strings.Contains(plainWide, "> LOCAL SESSIONS") ||
		!strings.Contains(plainWide, "> Claude") {
		t.Fatalf("wide layout lacks non-color focus markers:\n%s", plainWide)
	}

	model.width = 80
	model.height = 24
	stacked := model.render()
	if lipgloss.Width(stacked) != 80 || lipgloss.Height(stacked) != 24 {
		t.Fatalf("stacked dimensions = %dx%d", lipgloss.Width(stacked), lipgloss.Height(stacked))
	}
	if sameLine(stacked, "LOCAL SESSIONS", "SHARED CONTEXTS") {
		t.Fatalf("narrow layout did not stack panels:\n%s", stripANSI(stacked))
	}
}

func TestRenderSmallTerminalMessage(t *testing.T) {
	model := readyModel()
	model.width = 50
	model.height = 16
	rendered := stripANSI(model.render())
	if !strings.Contains(rendered, "ctx needs at least 60x18") ||
		!strings.Contains(rendered, "current terminal: 50x16") {
		t.Fatalf("rendered = %q", rendered)
	}
}

func TestRenderSessionPeek(t *testing.T) {
	model := readyModel()
	model.height = 36
	model.previewKey = "local:/sessions/claude.jsonl"
	model.preview = preview.Summary{
		CurrentRequest: "Improve the session details with a concise preview.",
		EventCount:     42,
		UserTurns:      6,
		AgentTurns:     8,
		ToolCalls:      12,
		Tools:          []string{"Bash", "Edit", "Read"},
		Recent: []preview.Turn{
			{Role: "You", Text: "Can we make the metadata useful?"},
			{Role: "Agent", Text: "I am adding an extractive summary."},
		},
		Entries: []preview.Entry{
			{Role: "You", Kind: "message", Text: "Can we make the metadata useful?"},
			{Role: "Agent", Kind: "message", Text: "I am adding an extractive summary."},
		},
		TranscriptPage:  1,
		TranscriptPages: 3,
		TranscriptCount: 14,
	}

	rendered := stripANSI(model.render())
	for _, want := range []string{
		"SESSION PEEK",
		"Current   Improve the session details",
		"42 events · 6 you · 8 agent · 12 tool calls · Bash, Edit, Read",
		"14 entries · page 1/3 · v inspect",
		"RECENT TURNS",
		"You       Can we make the metadata useful?",
		"Agent     I am adding an extractive summary.",
		"Workspace /work/payments",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered preview lacks %q:\n%s", want, rendered)
		}
	}
}

func TestTranscriptModalRendersAndPaginates(t *testing.T) {
	model := readyModel()
	model.width = 80
	model.height = 24
	model.previewKey = "local:/sessions/claude.jsonl:page:1"
	model.previewPage = 1
	model.preview = preview.Summary{
		CurrentRequest:  "Inspect more history",
		TranscriptPage:  1,
		TranscriptPages: 3,
		TranscriptCount: 14,
		Entries: []preview.Entry{
			{Role: "You", Kind: "message", Text: "Please inspect the queue retry behavior in more detail."},
			{Role: "Tool", Kind: "tool_call", Text: "Called Bash"},
			{Role: "Tool", Kind: "tool_result", Text: "Result returned"},
			{Role: "Agent", Kind: "message", Text: "The retry path is bounded and observable."},
		},
	}

	command, handled := model.handleKey("v")
	if !handled || command != nil || !model.showTranscript {
		t.Fatalf("transcript did not open: handled=%v command=%v show=%v", handled, command, model.showTranscript)
	}
	rendered := model.render()
	if lipgloss.Width(rendered) != 80 || lipgloss.Height(rendered) != 24 {
		t.Fatalf("transcript dimensions = %dx%d", lipgloss.Width(rendered), lipgloss.Height(rendered))
	}
	plain := stripANSI(rendered)
	for _, want := range []string{
		"SESSION TRANSCRIPT",
		"PAGE 1 / 3 · NEWEST · 14 ENTRIES",
		"Please inspect the queue retry behavior",
		"Called Bash",
		"PgUp older",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("transcript lacks %q:\n%s", want, plain)
		}
	}

	command, handled = model.handleKey("[")
	if !handled || command == nil || model.previewPage != 2 || !model.previewLoading {
		t.Fatalf("older page was not scheduled: page=%d loading=%v command=%v",
			model.previewPage, model.previewLoading, command)
	}
	if !model.showTranscript {
		t.Fatal("transcript closed while changing pages")
	}
	if model.preview.TranscriptPage != 2 || model.preview.TranscriptPages != 3 ||
		model.preview.TranscriptCount != 14 {
		t.Fatalf("loading page metadata = page %d/%d, count %d",
			model.preview.TranscriptPage, model.preview.TranscriptPages, model.preview.TranscriptCount)
	}
	if rendered = stripANSI(model.render()); !strings.Contains(rendered, "PAGE 2 / 3") ||
		!strings.Contains(rendered, "Loading transcript page") {
		t.Fatalf("loading transcript lost page context:\n%s", rendered)
	}
	model.handleKey("esc")
	if model.showTranscript {
		t.Fatal("escape did not close transcript")
	}
}

func TestRenderTranscriptEntryBoundsAndIndentsWrappedText(t *testing.T) {
	const width = 38
	lines := renderTranscriptEntry(preview.Entry{
		Role: "Agent",
		Text: "This intentionally long response should wrap and then be clipped without escaping the modal.",
	}, width, 2)
	if len(lines) != 2 {
		t.Fatalf("rendered %d lines, want 2: %#v", len(lines), lines)
	}
	for _, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line width = %d, want <= %d: %q", got, width, line)
		}
	}
	if !strings.HasPrefix(lines[1], strings.Repeat(" ", lipgloss.Width("       Agent   "))) {
		t.Fatalf("continuation is not indented: %q", lines[1])
	}
	if !strings.HasSuffix(lines[1], "…") {
		t.Fatalf("clipped continuation lacks ellipsis: %q", lines[1])
	}
}

func TestIntroAnimatesAndCanBeSkipped(t *testing.T) {
	model := readyModel()
	model.showIntro = true
	if duration := time.Duration(introFrameCount) * introFrameDelay; duration < 1700*time.Millisecond {
		t.Fatalf("intro duration = %s, want roughly two seconds", duration)
	}
	rendered := stripANSI(model.render())
	if !strings.Contains(rendered, "context travels better together") ||
		!strings.Contains(rendered, "/\\_/\\") {
		t.Fatalf("intro art was not rendered:\n%s", rendered)
	}
	if lipgloss.Width(model.render()) != model.width ||
		lipgloss.Height(model.render()) != model.height {
		t.Fatalf("intro dimensions = %dx%d", lipgloss.Width(model.render()), lipgloss.Height(model.render()))
	}

	if command, handled := model.handleKey("x"); !handled || command != nil || model.showIntro {
		t.Fatalf("intro was not skipped: handled=%v command=%v show=%v", handled, command, model.showIntro)
	}

	model.showIntro = true
	model.introFrame = introFrameCount - 1
	model.Update(introTickMsg{})
	if model.showIntro {
		t.Fatal("intro did not finish after its last frame")
	}
}

func TestHelpFitsMinimumTerminal(t *testing.T) {
	model := readyModel()
	model.width = 60
	model.height = 18
	baseLines := strings.Split(stripANSI(model.render()), "\n")
	if !strings.Contains(baseLines[len(baseLines)-1], "q quit") {
		t.Fatalf("minimum-width footer = %q\n%s", baseLines[len(baseLines)-1], strings.Join(baseLines, "\n"))
	}
	model.showHelp = true
	rendered := model.render()
	if lipgloss.Width(rendered) != 60 || lipgloss.Height(rendered) != 18 {
		t.Fatalf("help dimensions = %dx%d", lipgloss.Width(rendered), lipgloss.Height(rendered))
	}
	plain := stripANSI(rendered)
	if !strings.Contains(plain, "CTX KEYBOARD REFERENCE") ||
		!strings.Contains(plain, "q / Ctrl+C") {
		t.Fatalf("compact help was clipped:\n%s", plain)
	}
}

func TestKeyboardFilteringAndScopeToggle(t *testing.T) {
	model := readyModel()
	model.Update(keyPress("/"))
	model.Update(keyPress("c"))
	model.Update(keyPress("o"))
	model.Update(keyPress("d"))
	model.Update(keyPress("e"))
	model.Update(keyPress("x"))
	model.Update(specialKey(tea.KeyEnter))
	if model.localFilter != "codex" || model.filtering {
		t.Fatalf("filter = %q, filtering = %v", model.localFilter, model.filtering)
	}
	filtered := model.filteredLocal()
	if len(filtered) != 1 || filtered[0].SourceAgent != "codex-cli" {
		t.Fatalf("filtered local sessions = %+v", filtered)
	}

	command, handled := model.handleKey("a")
	if !handled || command == nil || !model.allLocal || !model.localLoading {
		t.Fatalf("scope toggle did not start a global reload: %+v", model)
	}
}

func TestActionResults(t *testing.T) {
	model := readyModel()
	command, handled := model.handleKey("h")
	if !handled || command == nil {
		t.Fatal("host shortcut was not handled")
	}
	if model.result.Action != ActionHost || model.result.SourcePath != "/sessions/claude.jsonl" {
		t.Fatalf("host result = %+v", model.result)
	}

	model = readyModel()
	model.focus = sharedPanel
	command, handled = model.handleKey("f")
	if !handled || command == nil {
		t.Fatal("follow shortcut was not handled")
	}
	if model.result.Action != ActionTail || !model.result.Follow ||
		model.result.Locator != "ctx://dev-laptop/payments" {
		t.Fatalf("follow result = %+v", model.result)
	}

	model = readyModel()
	model.focus = sharedPanel
	model.status.Agents = nil
	command, handled = model.handleKey("c")
	if !handled || command != nil {
		t.Fatalf("disabled continue returned command %v", command)
	}
	if !strings.Contains(model.notice, "Install Claude Code or Codex") {
		t.Fatalf("notice = %q", model.notice)
	}
}

func TestActionModalAndHelp(t *testing.T) {
	model := readyModel()
	model.focus = sharedPanel
	model.handleKey("enter")
	if !model.showActions || len(model.actionOptions()) != 3 {
		t.Fatalf("action modal state = %+v", model)
	}
	model.handleActionKey("down")
	if !strings.Contains(stripANSI(model.renderActionModal()), "> Follow updates") {
		t.Fatal("action modal lacks a non-color selection marker")
	}
	model.handleActionKey("enter")
	if model.result.Action != ActionTail || !model.result.Follow {
		t.Fatalf("modal result = %+v", model.result)
	}

	model = readyModel()
	model.handleKey("?")
	if !model.showHelp || !strings.Contains(stripANSI(model.render()), "CTX KEYBOARD REFERENCE") {
		t.Fatal("help modal was not rendered")
	}
	model.handleKey("esc")
	if model.showHelp {
		t.Fatal("help modal did not close")
	}

	model.handleKey("?")
	command, handled := model.handleKey("q")
	if !handled || command == nil {
		t.Fatal("q did not quit from the help modal")
	}
}

func TestStaleLoaderMessagesAreIgnored(t *testing.T) {
	model := readyModel()
	original := append([]LocalSession(nil), model.local...)
	model.localSeq = 2
	model.Update(localLoadedMsg{
		sequence: 1,
		sessions: []LocalSession{{SessionID: "stale"}},
	})
	if len(model.local) != len(original) || model.local[0].SessionID != original[0].SessionID {
		t.Fatalf("stale result replaced local inventory: %+v", model.local)
	}
}

func TestChangingSelectionCancelsDebouncedPreview(t *testing.T) {
	model := readyModel()
	model.previewKey = ""
	model.statusLoading = true
	firstCommand := model.schedulePreview()
	firstMessage, ok := firstCommand().(previewDebounceMsg)
	if !ok {
		t.Fatalf("preview command returned %T", firstCommand())
	}

	model.moveSelection(1)
	if next := model.schedulePreview(); next == nil {
		t.Fatal("new selection did not schedule another preview")
	}
	select {
	case <-firstMessage.ctx.Done():
	default:
		t.Fatal("previous preview context was not cancelled")
	}

	model.Update(previewLoadedMsg{
		sequence: firstMessage.sequence,
		summary:  preview.Summary{CurrentRequest: "stale"},
	})
	if model.preview.CurrentRequest == "stale" {
		t.Fatal("stale preview replaced the new selection")
	}
}

func TestLoaderCommandsUseConfiguredScope(t *testing.T) {
	loader := &fakeLoader{}
	model := NewModel(Config{Context: context.Background(), Loader: loader})
	model.allLocal = true
	message := model.loadLocal(4)()
	loaded, ok := message.(localLoadedMsg)
	if !ok || loaded.sequence != 4 || !loader.loadedAll {
		t.Fatalf("message = %#v, loadedAll = %v", message, loader.loadedAll)
	}
}

func TestRefreshCoalescesAndRestartsSpinner(t *testing.T) {
	model := readyModel()
	command := model.refresh()
	batch, ok := command().(tea.BatchMsg)
	if !ok || len(batch) != 4 {
		t.Fatalf("refresh command = %#v, want spinner plus three loaders", command())
	}
	if !model.statusLoading || !model.localLoading || !model.sharedLoading {
		t.Fatalf("refresh did not mark every loader active: %+v", model)
	}

	if repeated := model.refresh(); repeated != nil {
		t.Fatal("refresh started overlapping loader work")
	}
}

func TestScopeReloadRestartsOnlyStoppedSpinner(t *testing.T) {
	model := readyModel()
	command, handled := model.handleKey("a")
	if !handled || command == nil {
		t.Fatal("scope reload was not started")
	}
	batch, ok := command().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("scope command = %#v, want spinner plus local loader", command())
	}
	if repeated, handled := model.handleKey("a"); !handled || repeated != nil {
		t.Fatal("scope toggle started overlapping local discovery")
	}

	model = readyModel()
	model.sharedLoading = true
	command, handled = model.handleKey("a")
	if !handled || command == nil {
		t.Fatal("scope reload was not started while the shared spinner was active")
	}
	if _, duplicate := command().(tea.BatchMsg); duplicate {
		t.Fatal("scope reload scheduled a duplicate spinner loop")
	}
}

type fakeLoader struct {
	loadedAll   bool
	previewPage int
}

func (loader *fakeLoader) LoadStatus(context.Context) (Status, error) {
	return Status{}, nil
}

func (loader *fakeLoader) LoadLocal(_ context.Context, all bool) ([]LocalSession, error) {
	loader.loadedAll = all
	return nil, nil
}

func (loader *fakeLoader) LoadShared(context.Context) ([]SharedContext, error) {
	return nil, nil
}

func (loader *fakeLoader) LoadLocalPreview(_ context.Context, _ LocalSession, page int) (preview.Summary, error) {
	loader.previewPage = page
	return preview.Summary{}, nil
}

func (loader *fakeLoader) LoadSharedPreview(_ context.Context, _ SharedContext, page int) (preview.Summary, error) {
	loader.previewPage = page
	return preview.Summary{}, nil
}

func readyModel() *Model {
	now := time.Now()
	model := NewModel(Config{Version: "0.3.0", Loader: &fakeLoader{}})
	model.width = 120
	model.height = 36
	model.statusLoading = false
	model.localLoading = false
	model.sharedLoading = false
	model.status = Status{
		TailnetReady: true,
		TailnetName:  "dev-laptop",
		Agents:       []string{"Claude Code", "Codex"},
	}
	model.local = []LocalSession{
		{
			SourceAgent: "claude-code",
			SessionID:   "claude-1",
			Project:     "payments",
			CWD:         "/work/payments",
			Path:        "/sessions/claude.jsonl",
			ModifiedAt:  now,
		},
		{
			SourceAgent: "codex-cli",
			SessionID:   "codex-1",
			Project:     "ctx",
			CWD:         "/work/ctx",
			Path:        "/sessions/codex.jsonl",
			ModifiedAt:  now.Add(-time.Minute),
		},
	}
	model.shared = []SharedContext{{
		Name:        "payments",
		Owner:       "developer",
		Node:        "dev-laptop",
		SourceAgent: "claude-code",
		Project:     "payments",
		Revision:    "abc123",
		UpdatedAt:   now,
	}}
	return model
}

func keyPress(value string) tea.KeyPressMsg {
	runes := []rune(value)
	return tea.KeyPressMsg(tea.Key{Code: runes[0], Text: value})
}

func specialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9:;]*m`)

func stripANSI(value string) string {
	return ansiPattern.ReplaceAllString(value, "")
}

func sameLine(value, left, right string) bool {
	for _, line := range strings.Split(stripANSI(value), "\n") {
		if strings.Contains(line, left) && strings.Contains(line, right) {
			return true
		}
	}
	return false
}
