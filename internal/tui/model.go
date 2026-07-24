package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type ActionKind string

const (
	ActionNone     ActionKind = ""
	ActionHost     ActionKind = "host"
	ActionTail     ActionKind = "tail"
	ActionContinue ActionKind = "continue"
)

type Result struct {
	Action     ActionKind
	SourcePath string
	Locator    string
	Follow     bool
}

type Status struct {
	TailnetReady bool
	TailnetName  string
	TailnetError string
	Agents       []string
}

type LocalSession struct {
	SourceAgent string
	SessionID   string
	Project     string
	CWD         string
	Path        string
	ModifiedAt  time.Time
}

type SharedContext struct {
	Name        string
	Owner       string
	Node        string
	SourceAgent string
	Project     string
	Revision    string
	UpdatedAt   time.Time
}

func (s SharedContext) Locator() string {
	return fmt.Sprintf("ctx://%s/%s", s.Node, s.Name)
}

type SharedInventory struct {
	Contexts []SharedContext
	Warning  string
}

type Loader interface {
	LoadStatus(context.Context) (Status, error)
	LoadLocal(context.Context, bool) ([]LocalSession, error)
	LoadShared(context.Context) (SharedInventory, error)
}

type Config struct {
	Context context.Context
	Version string
	Loader  Loader
}

type panel int

const (
	localPanel panel = iota
	sharedPanel
)

type actionOption struct {
	label   string
	action  ActionKind
	follow  bool
	enabled bool
	reason  string
}

type statusLoadedMsg struct {
	sequence int
	status   Status
	err      error
}

type localLoadedMsg struct {
	sequence int
	sessions []LocalSession
	err      error
}

type sharedLoadedMsg struct {
	sequence  int
	inventory SharedInventory
	err       error
}

type Model struct {
	ctx     context.Context
	loader  Loader
	version string

	width  int
	height int
	dark   bool
	styles styles

	focus panel

	status        Status
	statusLoading bool
	statusError   string
	statusSeq     int

	local        []LocalSession
	localLoading bool
	localError   string
	localSeq     int
	allLocal     bool
	localIndex   int
	localFilter  string

	shared        []SharedContext
	sharedLoading bool
	sharedError   string
	sharedWarning string
	sharedSeq     int
	sharedIndex   int
	sharedFilter  string

	filterInput    textinput.Model
	filtering      bool
	filterOriginal string

	spinner spinner.Model

	showHelp    bool
	showActions bool
	actionIndex int
	notice      string
	result      Result
}

func NewModel(config Config) *Model {
	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}
	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "filter"
	filter.CharLimit = 160
	filter.SetVirtualCursor(true)

	progress := spinner.New()
	progress.Spinner = spinner.MiniDot

	model := &Model{
		ctx:           ctx,
		loader:        config.Loader,
		version:       config.Version,
		dark:          true,
		styles:        newStyles(true),
		filterInput:   filter,
		spinner:       progress,
		statusLoading: true,
		localLoading:  true,
		sharedLoading: true,
		statusSeq:     1,
		localSeq:      1,
		sharedSeq:     1,
	}
	return model
}

func Run(config Config, input io.Reader, output io.Writer) (Result, error) {
	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	config.Context = runCtx

	program := tea.NewProgram(
		NewModel(config),
		tea.WithInput(input),
		tea.WithOutput(output),
	)
	final, err := program.Run()
	if err != nil {
		return Result{}, err
	}
	model, ok := final.(*Model)
	if !ok {
		return Result{}, fmt.Errorf("unexpected final TUI model %T", final)
	}
	return model.result, nil
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		tea.RequestBackgroundColor,
		m.spinner.Tick,
		m.loadStatus(m.statusSeq),
		m.loadLocal(m.localSeq),
		m.loadShared(m.sharedSeq),
	)
}

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd

	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeFilter()
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.styles = newStyles(m.dark)
		m.applyComponentStyles()
	case statusLoadedMsg:
		if msg.sequence == m.statusSeq {
			m.statusLoading = false
			m.status = msg.status
			m.statusError = errorText(msg.err)
		}
	case localLoadedMsg:
		if msg.sequence == m.localSeq {
			m.localLoading = false
			m.local = msg.sessions
			m.localError = errorText(msg.err)
			m.clampSelection()
		}
	case sharedLoadedMsg:
		if msg.sequence == m.sharedSeq {
			m.sharedLoading = false
			m.shared = msg.inventory.Contexts
			m.sharedWarning = msg.inventory.Warning
			m.sharedError = errorText(msg.err)
			m.clampSelection()
		}
	case tea.KeyPressMsg:
		if command, handled := m.handleKey(msg.String()); handled {
			return m, command
		}
	}

	if m.filtering {
		updated, command := m.filterInput.Update(message)
		m.filterInput = updated
		m.setActiveFilter(updated.Value())
		m.clampSelection()
		commands = append(commands, command)
	}

	if m.anyLoading() {
		updated, command := m.spinner.Update(message)
		m.spinner = updated
		commands = append(commands, command)
	}

	return m, tea.Batch(commands...)
}

func (m *Model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	view.WindowTitle = "ctx"
	return view
}

func (m *Model) handleKey(key string) (tea.Cmd, bool) {
	if key == "ctrl+c" {
		m.result = Result{}
		return tea.Quit, true
	}
	if m.showHelp {
		switch key {
		case "esc", "?":
			m.showHelp = false
			return nil, true
		case "q":
			m.result = Result{}
			return tea.Quit, true
		default:
			return nil, true
		}
	}
	if m.showActions {
		return m.handleActionKey(key), true
	}
	if m.filtering {
		switch key {
		case "enter":
			m.finishFilter(true)
			return nil, true
		case "esc":
			m.finishFilter(false)
			return nil, true
		default:
			return nil, false
		}
	}

	switch key {
	case "q":
		m.result = Result{}
		return tea.Quit, true
	case "?":
		m.showHelp = true
	case "tab", "right", "l":
		m.focus = (m.focus + 1) % 2
		m.notice = ""
	case "shift+tab", "left":
		m.focus = (m.focus + 1) % 2
		m.notice = ""
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "pgup":
		m.moveSelection(-5)
	case "pgdown":
		m.moveSelection(5)
	case "/":
		m.startFilter()
		return m.filterInput.Focus(), true
	case "a":
		m.allLocal = !m.allLocal
		m.localIndex = 0
		m.localSeq++
		m.localLoading = true
		m.localError = ""
		m.notice = ""
		return m.loadLocal(m.localSeq), true
	case "r":
		return m.refresh(), true
	case "enter":
		m.openActions()
	case "h":
		return m.runDirect(ActionHost, false), true
	case "t":
		return m.runDirect(ActionTail, false), true
	case "f":
		return m.runDirect(ActionTail, true), true
	case "c":
		return m.runDirect(ActionContinue, false), true
	default:
		return nil, false
	}
	return nil, true
}

func (m *Model) handleActionKey(key string) tea.Cmd {
	options := m.actionOptions()
	switch key {
	case "esc":
		m.showActions = false
	case "q":
		m.result = Result{}
		return tea.Quit
	case "up", "k":
		m.actionIndex = wrap(m.actionIndex-1, len(options))
	case "down", "j", "tab":
		m.actionIndex = wrap(m.actionIndex+1, len(options))
	case "enter":
		if len(options) == 0 {
			m.showActions = false
			return nil
		}
		option := options[m.actionIndex]
		if !option.enabled {
			m.notice = option.reason
			m.showActions = false
			return nil
		}
		m.chooseAction(option)
		return tea.Quit
	}
	return nil
}

func (m *Model) runDirect(kind ActionKind, follow bool) tea.Cmd {
	for _, option := range m.actionOptions() {
		if option.action != kind || option.follow != follow {
			continue
		}
		if !option.enabled {
			m.notice = option.reason
			return nil
		}
		m.chooseAction(option)
		return tea.Quit
	}
	m.notice = "That action is not available for the selected panel."
	return nil
}

func (m *Model) chooseAction(option actionOption) {
	switch m.focus {
	case localPanel:
		session, ok := m.selectedLocal()
		if ok {
			m.result = Result{Action: option.action, SourcePath: session.Path}
		}
	case sharedPanel:
		shared, ok := m.selectedShared()
		if ok {
			m.result = Result{
				Action:  option.action,
				Locator: shared.Locator(),
				Follow:  option.follow,
			}
		}
	}
}

func (m *Model) actionOptions() []actionOption {
	if m.focus == localPanel {
		_, selected := m.selectedLocal()
		enabled := selected && m.status.TailnetReady
		reason := ""
		if !selected {
			reason = "Select a local session first."
		} else if !m.status.TailnetReady {
			reason = "Hosting requires a connected Tailscale tailnet."
		}
		return []actionOption{{
			label: "Host selected session", action: ActionHost, enabled: enabled, reason: reason,
		}}
	}

	_, selected := m.selectedShared()
	continueEnabled := selected && len(m.status.Agents) > 0
	continueReason := ""
	if !selected {
		continueReason = "Select a shared context first."
	} else if len(m.status.Agents) == 0 {
		continueReason = "Install Claude Code or Codex before continuing a context."
	}
	return []actionOption{
		{label: "Tail once", action: ActionTail, enabled: selected, reason: "Select a shared context first."},
		{label: "Follow updates", action: ActionTail, follow: true, enabled: selected, reason: "Select a shared context first."},
		{label: "Continue with an agent", action: ActionContinue, enabled: continueEnabled, reason: continueReason},
	}
}

func (m *Model) openActions() {
	options := m.actionOptions()
	if len(options) == 0 {
		return
	}
	m.actionIndex = 0
	for index, option := range options {
		if option.enabled {
			m.actionIndex = index
			break
		}
	}
	m.showActions = true
	m.notice = ""
}

func (m *Model) refresh() tea.Cmd {
	m.statusSeq++
	m.localSeq++
	m.sharedSeq++
	m.statusLoading = true
	m.localLoading = true
	m.sharedLoading = true
	m.statusError = ""
	m.localError = ""
	m.sharedError = ""
	m.sharedWarning = ""
	m.notice = ""
	return tea.Batch(
		m.loadStatus(m.statusSeq),
		m.loadLocal(m.localSeq),
		m.loadShared(m.sharedSeq),
	)
}

func (m *Model) loadStatus(sequence int) tea.Cmd {
	return func() tea.Msg {
		if m.loader == nil {
			return statusLoadedMsg{sequence: sequence, err: fmt.Errorf("TUI status loader is unavailable")}
		}
		status, err := m.loader.LoadStatus(m.ctx)
		return statusLoadedMsg{sequence: sequence, status: status, err: err}
	}
}

func (m *Model) loadLocal(sequence int) tea.Cmd {
	all := m.allLocal
	return func() tea.Msg {
		if m.loader == nil {
			return localLoadedMsg{sequence: sequence, err: fmt.Errorf("local session loader is unavailable")}
		}
		sessions, err := m.loader.LoadLocal(m.ctx, all)
		return localLoadedMsg{sequence: sequence, sessions: sessions, err: err}
	}
}

func (m *Model) loadShared(sequence int) tea.Cmd {
	return func() tea.Msg {
		if m.loader == nil {
			return sharedLoadedMsg{sequence: sequence, err: fmt.Errorf("shared context loader is unavailable")}
		}
		inventory, err := m.loader.LoadShared(m.ctx)
		return sharedLoadedMsg{sequence: sequence, inventory: inventory, err: err}
	}
}

func (m *Model) startFilter() {
	m.filterOriginal = m.activeFilter()
	m.filterInput.SetValue(m.filterOriginal)
	m.filtering = true
	m.resizeFilter()
	m.applyComponentStyles()
}

func (m *Model) finishFilter(apply bool) {
	if !apply {
		m.setActiveFilter(m.filterOriginal)
	}
	m.filtering = false
	m.filterInput.Blur()
	m.clampSelection()
}

func (m *Model) activeFilter() string {
	if m.focus == localPanel {
		return m.localFilter
	}
	return m.sharedFilter
}

func (m *Model) setActiveFilter(value string) {
	if m.focus == localPanel {
		m.localFilter = value
	} else {
		m.sharedFilter = value
	}
}

func (m *Model) resizeFilter() {
	width := m.width/2 - 8
	if m.width < 100 {
		width = m.width - 8
	}
	m.filterInput.SetWidth(max(10, width))
}

func (m *Model) applyComponentStyles() {
	if m.dark {
		m.filterInput.SetStyles(textinput.DefaultDarkStyles())
	} else {
		m.filterInput.SetStyles(textinput.DefaultLightStyles())
	}
	m.spinner.Style = m.styles.spinner
}

func (m *Model) moveSelection(delta int) {
	if m.focus == localPanel {
		m.localIndex = clamp(m.localIndex+delta, len(m.filteredLocal()))
	} else {
		m.sharedIndex = clamp(m.sharedIndex+delta, len(m.filteredShared()))
	}
	m.notice = ""
}

func (m *Model) clampSelection() {
	m.localIndex = clamp(m.localIndex, len(m.filteredLocal()))
	m.sharedIndex = clamp(m.sharedIndex, len(m.filteredShared()))
}

func (m *Model) selectedLocal() (LocalSession, bool) {
	items := m.filteredLocal()
	if len(items) == 0 {
		return LocalSession{}, false
	}
	return items[m.localIndex], true
}

func (m *Model) selectedShared() (SharedContext, bool) {
	items := m.filteredShared()
	if len(items) == 0 {
		return SharedContext{}, false
	}
	return items[m.sharedIndex], true
}

func (m *Model) filteredLocal() []LocalSession {
	query := strings.ToLower(strings.TrimSpace(m.localFilter))
	if query == "" {
		return m.local
	}
	var filtered []LocalSession
	for _, session := range m.local {
		searchable := strings.ToLower(strings.Join([]string{
			session.Project, session.SourceAgent, session.SessionID, session.Path, session.CWD,
		}, " "))
		if strings.Contains(searchable, query) {
			filtered = append(filtered, session)
		}
	}
	return filtered
}

func (m *Model) filteredShared() []SharedContext {
	query := strings.ToLower(strings.TrimSpace(m.sharedFilter))
	if query == "" {
		return m.shared
	}
	var filtered []SharedContext
	for _, shared := range m.shared {
		searchable := strings.ToLower(strings.Join([]string{
			shared.Node, shared.Name, shared.Project, shared.SourceAgent, shared.Owner,
		}, " "))
		if strings.Contains(searchable, query) {
			filtered = append(filtered, shared)
		}
	}
	return filtered
}

func (m *Model) anyLoading() bool {
	return m.statusLoading || m.localLoading || m.sharedLoading
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func clamp(value, count int) int {
	if count <= 0 || value < 0 {
		return 0
	}
	if value >= count {
		return count - 1
	}
	return value
}

func wrap(value, count int) int {
	if count <= 0 {
		return 0
	}
	for value < 0 {
		value += count
	}
	return value % count
}
