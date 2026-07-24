package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

func TestHelpFitsMinimumTerminal(t *testing.T) {
	model := readyModel()
	model.width = 60
	model.height = 18
	baseLines := strings.Split(stripANSI(model.render()), "\n")
	if !strings.Contains(baseLines[len(baseLines)-1], "q quit") {
		t.Fatalf("minimum-width footer = %q", baseLines[len(baseLines)-1])
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
	loadedAll bool
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
