package agenthud

import (
	"fmt"
	"time"

	"tmux-menu/internal/agentstatus"
	"tmux-menu/internal/tmux"
)

const defaultModelTerminalBytes = 256 << 10

type Identity struct {
	sessionID   string
	windowID    string
	paneID      string
	panePID     int
	providerPID int
}

func NewIdentity(sessionID, windowID, paneID string, panePID, providerPID int) (Identity, error) {
	if !tmux.IsCanonicalID(sessionID, '$') || !tmux.IsCanonicalID(windowID, '@') || !tmux.IsCanonicalID(paneID, '%') || panePID <= 0 || providerPID < 0 {
		return Identity{}, fmt.Errorf("invalid stable tmux identity")
	}
	return Identity{sessionID: sessionID, windowID: windowID, paneID: paneID, panePID: panePID, providerPID: providerPID}, nil
}

func (i Identity) SessionID() string { return i.sessionID }
func (i Identity) WindowID() string  { return i.windowID }
func (i Identity) PaneID() string    { return i.paneID }
func (i Identity) PanePID() int      { return i.panePID }
func (i Identity) ProviderPID() int  { return i.providerPID }
func (i Identity) Valid() bool {
	return tmux.IsCanonicalID(i.sessionID, '$') && tmux.IsCanonicalID(i.windowID, '@') && tmux.IsCanonicalID(i.paneID, '%') && i.panePID > 0 && i.providerPID >= 0
}

type AgentPresentation struct {
	Identity       Identity
	ProviderKind   agentstatus.Provider
	Provider       Text
	Status         agentstatus.State
	Session        Text
	Thread         Text
	Workdir        Text
	Source         Text
	Fresh          bool
	SessionColor   Color
	CurrentThread  bool
	Children       int
	TurnStartedAt  time.Time
	StateChangedAt time.Time
	LastEventAt    time.Time
}

type Agent struct {
	identity       Identity
	providerKind   agentstatus.Provider
	provider       Text
	status         agentstatus.State
	session        Text
	thread         Text
	workdir        Text
	source         Text
	fresh          bool
	sessionColor   Color
	currentThread  bool
	children       int
	turnStartedAt  time.Time
	stateChangedAt time.Time
	lastEventAt    time.Time
}

func NewAgent(value AgentPresentation) Agent {
	status := value.Status
	if !validStatus(status) {
		status = agentstatus.StateUnknown
	}
	return Agent{
		identity: value.Identity, providerKind: value.ProviderKind, provider: value.Provider, status: status,
		session: value.Session, thread: value.Thread, workdir: value.Workdir,
		source: value.Source, fresh: value.Fresh, sessionColor: value.SessionColor,
		currentThread: value.CurrentThread, children: max(value.Children, 0),
		turnStartedAt: value.TurnStartedAt, stateChangedAt: value.StateChangedAt, lastEventAt: value.LastEventAt,
	}
}

func validStatus(value agentstatus.State) bool {
	switch value {
	case agentstatus.StateAttention, agentstatus.StateWorking, agentstatus.StateCompleted, agentstatus.StateWaiting, agentstatus.StateUnknown:
		return true
	default:
		return false
	}
}

func (a Agent) Identity() Identity        { return a.identity }
func (a Agent) Status() agentstatus.State { return a.status }
func (a Agent) Source() Text              { return a.source }
func (a Agent) Fresh() bool               { return a.fresh }

type TerminalUpdate struct {
	Identity Identity
	Terminal Terminal
	Failed   bool
	Failure  Text
}

type RefreshResult struct {
	Generation uint64
	Now        time.Time
	Agents     []Agent
	Captures   []TerminalUpdate
	Failed     bool
	Failure    Text
}

type CaptureTarget struct {
	Identity Identity
}

type RefreshRequest struct {
	Generation uint64
	Targets    []CaptureTarget
}

type Summary struct {
	Total     int
	Attention int
	Working   int
	Completed int
	Waiting   int
	Unknown   int
}

type ModelOptions struct {
	Width            int
	Height           int
	Now              time.Time
	MaxTerminalBytes int
	Theme            Theme
}

type tailState struct {
	identity Identity
	terminal Terminal
	stale    bool
	failure  Text
}

type Model struct {
	width            int
	height           int
	now              time.Time
	maxTerminalBytes int
	slots            []string
	agents           map[string]Agent
	tails            map[string]tailState
	selected         string
	focused          bool
	fullHelp         bool
	latestGeneration uint64
	refreshFailure   Text
	theme            Theme
}

func NewModel(options ModelOptions) Model {
	limit := options.MaxTerminalBytes
	if limit <= 0 {
		limit = defaultModelTerminalBytes
	}
	theme := options.Theme
	if !theme.initialized {
		theme = DefaultTheme()
	}
	return Model{
		width: max(options.Width, 1), height: max(options.Height, 1), now: options.Now,
		maxTerminalBytes: limit, agents: make(map[string]Agent), tails: make(map[string]tailState), theme: theme,
	}
}

func (m *Model) BeginRefresh() RefreshRequest {
	m.latestGeneration++
	visible := m.visiblePaneIDs()
	targets := make([]CaptureTarget, 0, len(visible))
	for _, paneID := range visible {
		agent := m.agents[paneID]
		targets = append(targets, CaptureTarget{Identity: agent.identity})
	}
	return RefreshRequest{Generation: m.latestGeneration, Targets: targets}
}

func (m *Model) ApplyRefresh(result RefreshResult) bool {
	if result.Generation != m.latestGeneration {
		return false
	}
	if !result.Now.IsZero() {
		m.now = result.Now
	}
	if result.Failed {
		m.refreshFailure = result.Failure
		for _, paneID := range m.visiblePaneIDs() {
			tail := m.tails[paneID]
			tail.identity = m.agents[paneID].identity
			tail.stale = true
			tail.failure = result.Failure
			m.tails[paneID] = tail
		}
		m.evictHiddenTails()
		return true
	}
	m.refreshFailure = Text{}
	m.reconcile(result.Agents)
	m.evictHiddenTails()
	visible := stringSet(m.visiblePaneIDs())
	updates := make(map[string]TerminalUpdate, len(result.Captures))
	for _, update := range result.Captures {
		paneID := update.Identity.paneID
		agent, exists := m.agents[paneID]
		if !exists || agent.identity != update.Identity || !visible[paneID] {
			continue
		}
		updates[paneID] = update
	}

	remaining := m.maxTerminalBytes
	for _, paneID := range m.visiblePaneIDs() {
		tail, hasTail := m.tails[paneID]
		if update, ok := updates[paneID]; ok {
			if update.Failed {
				tail.identity = update.Identity
				tail.stale = true
				tail.failure = update.Failure
			} else {
				tail = tailState{identity: update.Identity, terminal: update.Terminal}
				hasTail = true
			}
		}
		if !hasTail && tail.terminal.RetainedBytes() == 0 && !tail.stale {
			continue
		}
		tail.terminal = retainTerminal(tail.terminal, remaining)
		remaining -= tail.terminal.RetainedBytes()
		m.tails[paneID] = tail
	}
	return true
}

func (m *Model) reconcile(values []Agent) {
	next := make(map[string]Agent, len(values))
	incoming := make([]string, 0, len(values))
	for _, agent := range values {
		paneID := agent.identity.paneID
		if !agent.identity.Valid() {
			continue
		}
		if _, duplicate := next[paneID]; duplicate {
			continue
		}
		if previous, exists := m.agents[paneID]; exists && previous.identity != agent.identity {
			delete(m.tails, paneID)
		}
		next[paneID] = agent
		incoming = append(incoming, paneID)
	}
	stable := make([]string, 0, len(next))
	seen := make(map[string]bool, len(next))
	for _, paneID := range m.slots {
		if _, exists := next[paneID]; exists {
			stable = append(stable, paneID)
			seen[paneID] = true
		}
	}
	for _, paneID := range incoming {
		if !seen[paneID] {
			stable = append(stable, paneID)
		}
	}
	m.slots = stable
	m.agents = next
	if _, exists := next[m.selected]; !exists {
		m.focused = false
		m.selected = ""
		if len(stable) != 0 {
			m.selected = stable[0]
		}
	}
}

func retainTerminal(value Terminal, maxBytes int) Terminal {
	if maxBytes <= 0 {
		return Terminal{}
	}
	if value.RetainedBytes() <= maxBytes {
		return value
	}
	selected := make([]Text, 0, len(value.lines))
	remaining := maxBytes
	for index := len(value.lines) - 1; index >= 0 && remaining > 0; index-- {
		line := clipLine(value.lines[index].spans, int(^uint(0)>>1), remaining)
		selected = append(selected, line)
		remaining -= line.bytes
	}
	result := Terminal{lines: make([]Text, len(selected))}
	for index := range selected {
		line := selected[len(selected)-1-index]
		result.lines[index] = line
		result.bytes += line.bytes
	}
	return result
}

type Key string

const (
	KeyLeft          Key = "left"
	KeyRight         Key = "right"
	KeyUp            Key = "up"
	KeyDown          Key = "down"
	KeyPagePrevious  Key = "["
	KeyPageNext      Key = "]"
	KeyNextAttention Key = "n"
	KeyFocus         Key = "z"
	KeyHelp          Key = "?"
	KeyQuit          Key = "q"
	KeyEnter         Key = "enter"
	KeyOpenPicker    Key = "/"
)

type ActionKind int

const (
	ActionNone ActionKind = iota
	ActionQuit
	ActionSwitch
	ActionOpenPicker
)

type Action struct {
	kind     ActionKind
	identity Identity
}

func (a Action) Kind() ActionKind   { return a.kind }
func (a Action) Identity() Identity { return a.identity }

func (m *Model) HandleKey(key Key) Action {
	switch key {
	case "h", KeyLeft:
		m.move(-1, 0)
	case "l", KeyRight:
		m.move(1, 0)
	case "k", KeyUp:
		m.move(0, -1)
	case "j", KeyDown:
		m.move(0, 1)
	case KeyPagePrevious:
		m.changePage(-1)
	case KeyPageNext:
		m.changePage(1)
	case KeyNextAttention:
		m.nextAttention()
	case KeyFocus:
		if m.selected != "" {
			m.focused = !m.focused
			m.evictHiddenTails()
		}
	case KeyHelp:
		m.fullHelp = !m.fullHelp
		m.evictHiddenTails()
	case KeyQuit:
		return Action{kind: ActionQuit}
	case KeyEnter:
		if agent, ok := m.agents[m.selected]; ok {
			return Action{kind: ActionSwitch, identity: agent.identity}
		}
	case KeyOpenPicker:
		return Action{kind: ActionOpenPicker}
	}
	return Action{}
}

func (m *Model) move(dx, dy int) {
	if len(m.slots) == 0 {
		return
	}
	layout := m.overviewLayout()
	capacity := max(layout.Capacity(), 1)
	index := m.selectedIndex()
	pageStart := index / capacity * capacity
	local := index - pageStart
	column := local % max(layout.Columns, 1)
	row := local / max(layout.Columns, 1)
	nextColumn := column + dx
	nextRow := row + dy
	if nextColumn < 0 || nextColumn >= layout.Columns || nextRow < 0 || nextRow >= layout.Rows {
		return
	}
	next := pageStart + nextRow*layout.Columns + nextColumn
	if next >= len(m.slots) {
		return
	}
	m.selected = m.slots[next]
	m.evictHiddenTails()
}

func (m *Model) changePage(delta int) {
	pages := m.PageCount()
	if pages <= 1 {
		return
	}
	capacity := max(m.overviewLayout().Capacity(), 1)
	index := m.selectedIndex()
	offset := index % capacity
	page := (m.Page() + delta + pages) % pages
	next := min(page*capacity+offset, len(m.slots)-1)
	m.selected = m.slots[next]
	m.evictHiddenTails()
}

func (m *Model) nextAttention() {
	if len(m.slots) == 0 {
		return
	}
	start := m.selectedIndex()
	for offset := 1; offset <= len(m.slots); offset++ {
		index := (start + offset) % len(m.slots)
		if m.agents[m.slots[index]].status == agentstatus.StateAttention {
			m.selected = m.slots[index]
			m.evictHiddenTails()
			return
		}
	}
}

func (m *Model) SetSize(width, height int) {
	m.width, m.height = max(width, 1), max(height, 1)
	m.evictHiddenTails()
}

func (m *Model) SetNow(now time.Time)  { m.now = now }
func (m Model) Size() Size             { return Size{Width: m.width, Height: m.height} }
func (m Model) Focused() bool          { return m.focused }
func (m Model) FullHelp() bool         { return m.fullHelp }
func (m Model) SelectedPaneID() string { return m.selected }
func (m Model) AgentCount() int        { return len(m.slots) }
func (m Model) SlotPaneIDs() []string  { return append([]string(nil), m.slots...) }

func (m Model) Page() int {
	capacity := max(m.overviewLayout().Capacity(), 1)
	return m.selectedIndex() / capacity
}

func (m Model) PageCount() int {
	if len(m.slots) == 0 {
		return 1
	}
	capacity := max(m.overviewLayout().Capacity(), 1)
	return (len(m.slots) + capacity - 1) / capacity
}

func (m Model) Summary() Summary {
	result := Summary{Total: len(m.slots)}
	for _, paneID := range m.slots {
		switch m.agents[paneID].status {
		case agentstatus.StateAttention:
			result.Attention++
		case agentstatus.StateWorking:
			result.Working++
		case agentstatus.StateCompleted:
			result.Completed++
		case agentstatus.StateWaiting:
			result.Waiting++
		default:
			result.Unknown++
		}
	}
	return result
}

func (m Model) TerminalPlain(paneID string) (string, bool) {
	tail, ok := m.tails[paneID]
	return tail.terminal.Plain(), ok
}

func (m Model) TailStale(paneID string) bool { return m.tails[paneID].stale }
func (m Model) TailCount() int               { return len(m.tails) }
func (m Model) RetainedTerminalBytes() int {
	total := 0
	for _, tail := range m.tails {
		total += tail.terminal.RetainedBytes()
	}
	return total
}

func (m Model) NeedsVisibleCapture() bool {
	for _, paneID := range m.visiblePaneIDs() {
		tail, ok := m.tails[paneID]
		if !ok || tail.identity != m.agents[paneID].identity {
			return true
		}
	}
	return false
}

func (m Model) currentLayout() Layout  { return ComputeLayout(m.width, m.height, m.focused, m.fullHelp) }
func (m Model) overviewLayout() Layout { return ComputeLayout(m.width, m.height, false, m.fullHelp) }

func (m Model) selectedIndex() int {
	for index, paneID := range m.slots {
		if paneID == m.selected {
			return index
		}
	}
	return 0
}

func (m Model) visiblePaneIDs() []string {
	if len(m.slots) == 0 {
		return nil
	}
	if m.focused {
		return []string{m.selected}
	}
	capacity := max(m.overviewLayout().Capacity(), 1)
	start := m.Page() * capacity
	end := min(start+capacity, len(m.slots))
	return append([]string(nil), m.slots[start:end]...)
}

func (m *Model) evictHiddenTails() {
	visible := stringSet(m.visiblePaneIDs())
	for paneID := range m.tails {
		if !visible[paneID] {
			delete(m.tails, paneID)
		}
	}
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}
