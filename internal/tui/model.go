// Package tui is the full-screen interface for monitoring an MQTT broker.
package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/paul-ridgway/mqtt-monitor/internal/config"
	"github.com/paul-ridgway/mqtt-monitor/internal/mqttx"
)

// Config is what the UI needs to run.
type Config struct {
	Client  mqttx.Client
	Feed    *Feed
	Broker  string // shown in the status bar
	Version int
	Profile *config.Profile // publish history is recorded here; may be nil
	Buffer  int             // messages kept in memory
}

type pane int

const (
	paneTree pane = iota
	paneList
	paneDetail
)

type tickMsg time.Time

type opResultMsg struct {
	text string
	err  error
}

// Model is the Bubble Tea model.
type Model struct {
	cfg Config
	w   int
	h   int

	focus    pane
	showTree bool

	store *store
	tree  *tree

	view   []*entry
	cursor int
	offset int
	follow bool

	paused        bool
	pausedPending int

	flt         filter
	filtering   bool
	filterInput textinput.Model
	prevFilter  string

	treeSel    *node
	treeCursor int
	treeOffset int

	detail     viewport.Model
	detailFor  *entry
	detailW    int
	detailMode viewMode

	state    mqttx.State
	stateErr error
	subs     []mqttx.Subscription

	total     int
	rateHist  [5]int
	rateIdx   int
	lastTotal int
	rate      float64

	flash      string
	flashErr   bool
	flashUntil time.Time

	dialog      dialog
	lastPublish config.Publish
}

// New creates the model.
func New(cfg Config) *Model {
	fi := textinput.New()
	fi.Prompt = ""
	fi.Placeholder = "topic or payload text, or a +/# topic filter"
	fi.Cursor.SetMode(cursor.CursorStatic)
	m := &Model{
		cfg:         cfg,
		focus:       paneList,
		showTree:    true,
		store:       newStore(cfg.Buffer),
		tree:        newTree(),
		follow:      true,
		filterInput: fi,
		detail:      viewport.New(0, 0),
		state:       mqttx.StateConnecting,
		subs:        cfg.Client.Subscriptions(),
	}
	if cfg.Profile != nil && len(cfg.Profile.Publishes) > 0 {
		m.lastPublish = cfg.Profile.Publishes[0]
	}
	return m
}

// Run starts the UI and blocks until the user quits.
func Run(cfg Config) error {
	p := tea.NewProgram(New(cfg), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.cfg.Feed.next(), tick(), tea.SetWindowTitle("mqmon "+m.cfg.Broker))
}

func (m *Model) setFlash(text string, isErr bool) {
	m.flash = text
	m.flashErr = isErr
	m.flashUntil = time.Now().Add(5 * time.Second)
}

// --- message handling ---

func (m *Model) addMessage(msg *mqttx.Message) {
	e, dropped := m.store.add(msg)
	m.tree.add(e)
	m.total++
	if dropped > 0 {
		m.trimView()
	}
	if m.paused {
		m.pausedPending++
		return
	}
	if m.flt.match(e) {
		m.view = append(m.view, e)
		if m.follow {
			m.cursor = len(m.view) - 1
		}
	}
}

// trimView drops list entries that have fallen out of the store.
func (m *Model) trimView() {
	oldest := m.store.oldestSeq()
	i := sort.Search(len(m.view), func(i int) bool { return m.view[i].seq >= oldest })
	if i == 0 {
		return
	}
	m.view = append([]*entry(nil), m.view[i:]...)
	m.cursor -= i
	m.offset -= i
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// rebuildView re-applies the filter to the stored messages, keeping the
// selection where possible.
func (m *Model) rebuildView() {
	var selSeq = -1
	if e := m.selected(); e != nil {
		selSeq = e.seq
	}
	m.view = m.view[:0:0]
	for _, e := range m.store.msgs {
		if m.flt.match(e) {
			m.view = append(m.view, e)
		}
	}
	m.pausedPending = 0
	if m.follow || selSeq < 0 {
		m.cursor = len(m.view) - 1
	} else {
		m.cursor = sort.Search(len(m.view), func(i int) bool { return m.view[i].seq >= selSeq })
		if m.cursor >= len(m.view) {
			m.cursor = len(m.view) - 1
		}
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) setFilter(text string) {
	m.flt = newFilter(text)
	m.follow = true
	m.rebuildView()
}

// selected is the message highlighted in the list.
func (m *Model) selected() *entry {
	if m.cursor >= 0 && m.cursor < len(m.view) {
		return m.view[m.cursor]
	}
	return nil
}

// current is the message shown in the detail pane: the last message on the
// selected topic when the tree has focus, otherwise the list selection.
func (m *Model) current() *entry {
	if m.focus == paneTree {
		if m.treeSel != nil {
			return m.treeSel.last
		}
		return nil
	}
	return m.selected()
}

// currentTopic is the topic the user is looking at, if any.
func (m *Model) currentTopic() string {
	if m.focus == paneTree && m.treeSel != nil {
		return m.treeSel.full
	}
	if e := m.selected(); e != nil {
		return e.Topic
	}
	return ""
}

func (m *Model) publishSuggestions() []string {
	seen := map[string]bool{}
	var out []string
	if m.cfg.Profile != nil {
		for _, p := range m.cfg.Profile.Publishes {
			if !seen[p.Topic] {
				seen[p.Topic] = true
				out = append(out, p.Topic)
			}
		}
	}
	for _, s := range m.tree.suggestions() {
		if !strings.ContainsAny(s, "+#") && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// --- commands ---

func (m *Model) subscribeCmd(topic string, qos byte) tea.Cmd {
	c := m.cfg.Client
	return func() tea.Msg {
		if err := c.Subscribe(topic, qos); err != nil {
			return opResultMsg{err: fmt.Errorf("subscribe %s: %w", topic, err)}
		}
		return opResultMsg{text: fmt.Sprintf("Subscribed to %s (QoS %d)", topic, qos)}
	}
}

func (m *Model) replaceCmd(topic string, qos byte) tea.Cmd {
	c := m.cfg.Client
	return func() tea.Msg {
		if err := c.Subscribe(topic, qos); err != nil {
			return opResultMsg{err: fmt.Errorf("subscribe %s: %w", topic, err)}
		}
		for _, s := range c.Subscriptions() {
			if s.Topic != topic {
				if err := c.Unsubscribe(s.Topic); err != nil {
					return opResultMsg{err: fmt.Errorf("unsubscribe %s: %w", s.Topic, err)}
				}
			}
		}
		return opResultMsg{text: fmt.Sprintf("Now subscribed to %s only", topic)}
	}
}

func (m *Model) unsubscribeCmd(topic string) tea.Cmd {
	c := m.cfg.Client
	return func() tea.Msg {
		if err := c.Unsubscribe(topic); err != nil {
			return opResultMsg{err: fmt.Errorf("unsubscribe %s: %w", topic, err)}
		}
		return opResultMsg{text: "Unsubscribed from " + topic}
	}
}

func (m *Model) publishCmd(p config.Publish) tea.Cmd {
	c := m.cfg.Client
	if m.cfg.Profile != nil {
		m.cfg.Profile.RememberPublish(p)
	}
	return func() tea.Msg {
		err := c.Publish(mqttx.PublishOptions{Topic: p.Topic, Payload: []byte(p.Payload), QoS: p.QoS, Retain: p.Retain})
		if err != nil {
			return opResultMsg{err: fmt.Errorf("publish to %s: %w", p.Topic, err)}
		}
		what := humanBytes(len(p.Payload))
		if p.Payload == "" && p.Retain {
			return opResultMsg{text: "Cleared retained message on " + p.Topic}
		}
		r := ""
		if p.Retain {
			r = ", retained"
		}
		return opResultMsg{text: fmt.Sprintf("Published %s to %s (QoS %d%s)", what, p.Topic, p.QoS, r)}
	}
}

func copyCmd(text, what string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboard.WriteAll(text); err != nil {
			// Fall back to OSC 52, which most terminals (and tmux) support.
			termenv.DefaultOutput().Copy(text)
		}
		return opResultMsg{text: "Copied " + what}
	}
}

// --- update ---

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case batchMsg:
		for _, ev := range msg {
			switch ev := ev.(type) {
			case mqttx.MessageEvent:
				m.addMessage(ev.Message)
			case mqttx.StateEvent:
				m.handleState(ev)
			}
		}
		return m, m.cfg.Feed.next()

	case tickMsg:
		m.rateIdx = (m.rateIdx + 1) % len(m.rateHist)
		m.rateHist[m.rateIdx] = m.total - m.lastTotal
		m.lastTotal = m.total
		sum := 0
		for _, n := range m.rateHist {
			sum += n
		}
		m.rate = float64(sum) / float64(len(m.rateHist))
		return m, tick()

	case opResultMsg:
		m.subs = m.cfg.Client.Subscriptions()
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
		} else {
			m.setFlash(msg.text, false)
		}
		return m, nil

	case tea.MouseMsg:
		if m.dialog == nil {
			m.handleMouse(msg)
		}
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.dialog != nil {
			var cmd tea.Cmd
			m.dialog, cmd = m.dialog.Update(m, msg)
			return m, cmd
		}
		if m.filtering {
			return m, m.handleFilterKey(msg)
		}
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleState(ev mqttx.StateEvent) {
	prev := m.state
	if ev.State == mqttx.StateConnecting && prev == mqttx.StateDisconnected {
		// Keep showing why we disconnected while retrying.
		m.state = ev.State
		return
	}
	m.state = ev.State
	switch ev.State {
	case mqttx.StateConnected:
		m.stateErr = nil
		m.subs = m.cfg.Client.Subscriptions()
		if prev != mqttx.StateConnected {
			m.setFlash("Connected", false)
		}
	case mqttx.StateDisconnected:
		if ev.Err != nil {
			m.stateErr = ev.Err
			m.setFlash(ev.Err.Error(), true)
		}
	}
}

func (m *Model) handleFilterKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		m.filtering = false
		m.filterInput.Blur()
		return nil
	case "esc":
		m.filtering = false
		m.filterInput.Blur()
		m.setFilter(m.prevFilter)
		return nil
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	if v := m.filterInput.Value(); v != m.flt.text {
		m.setFilter(v)
	}
	return cmd
}

func (m *Model) panes() []pane {
	if m.treeWidth() > 0 {
		return []pane{paneTree, paneList, paneDetail}
	}
	return []pane{paneList, paneDetail}
}

func (m *Model) cycleFocus(dir int) {
	ps := m.panes()
	idx := 0
	for i, p := range ps {
		if p == m.focus {
			idx = i
		}
	}
	m.focus = ps[(idx+dir+len(ps))%len(ps)]
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	switch k {
	case "q":
		return tea.Quit
	case "tab":
		m.cycleFocus(1)
		return nil
	case "shift+tab":
		m.cycleFocus(-1)
		return nil
	case "?":
		m.dialog = helpDialog{}
		return nil
	case "s":
		m.dialog = newSubscribeDialog(m, "", false)
		return nil
	case "u":
		m.dialog = newSubscribeDialog(m, "", true)
		return nil
	case "p":
		p := m.lastPublish
		if t := m.currentTopic(); t != "" && t != p.Topic {
			p = config.Publish{Topic: t}
			if m.cfg.Profile != nil {
				for _, h := range m.cfg.Profile.Publishes {
					if h.Topic == t {
						p = h
						break
					}
				}
			}
		}
		m.dialog = newPublishDialog(m, p)
		return nil
	case "P":
		e := m.current()
		if e == nil {
			m.setFlash("No message selected", true)
			return nil
		}
		m.dialog = newPublishDialog(m, config.Publish{Topic: e.Topic, Payload: string(e.Payload), QoS: e.QoS, Retain: e.Retained})
		return nil
	case "D":
		e := m.current()
		if e == nil {
			m.setFlash("Select a topic that has a message first", true)
			return nil
		}
		t := e.Topic
		m.dialog = &confirmDialog{
			title: "Delete retained message",
			text:  "Publish an empty retained message to\n" + stTopic.Render(t) + "\nso the broker forgets its retained value?",
			yes:   m.publishCmd(config.Publish{Topic: t, Retain: true}),
		}
		return nil
	case "/":
		m.filtering = true
		m.prevFilter = m.flt.text
		m.filterInput.SetValue(m.flt.text)
		m.filterInput.CursorEnd()
		m.filterInput.Focus()
		return nil
	case "f":
		t := ""
		if m.focus == paneTree && m.treeSel != nil {
			t = m.treeSel.filterTopic()
		} else if e := m.selected(); e != nil {
			t = e.Topic
		}
		if t == "" || t == m.flt.text {
			m.setFilter("")
		} else {
			m.setFilter(t)
		}
		return nil
	case "esc":
		if m.flt.active() {
			m.setFilter("")
		}
		return nil
	case " ":
		m.paused = !m.paused
		if !m.paused {
			m.rebuildView()
		}
		return nil
	case "c":
		m.store.clear()
		m.tree = newTree()
		m.treeSel = nil
		m.view = nil
		m.cursor, m.offset, m.treeCursor, m.treeOffset = 0, 0, 0, 0
		m.pausedPending = 0
		m.follow = true
		m.setFlash("Cleared", false)
		return nil
	case "v":
		m.detailMode = (m.detailMode + 1) % 3
		m.detailFor = nil
		return nil
	case "y", "Y":
		e := m.current()
		if e == nil {
			return nil
		}
		if k == "Y" {
			return copyCmd(e.Topic, "topic")
		}
		return copyCmd(string(e.Payload), "payload ("+humanBytes(len(e.Payload))+")")
	case "t":
		m.showTree = !m.showTree
		if !m.showTree && m.focus == paneTree {
			m.focus = paneList
		}
		return nil
	}

	switch m.focus {
	case paneTree:
		m.treeKey(k)
	case paneList:
		m.listKey(k)
	case paneDetail:
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return cmd
	}
	return nil
}

func (m *Model) listPageSize() int {
	_, listH, _ := m.sizes()
	return max(listH-2, 1)
}

func (m *Model) moveList(delta int) {
	if len(m.view) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.view)-1 {
		m.cursor = len(m.view) - 1
		m.follow = true
	} else {
		m.follow = false
	}
}

func (m *Model) listKey(k string) {
	switch k {
	case "up", "k":
		m.moveList(-1)
	case "down", "j":
		m.moveList(1)
	case "pgup", "ctrl+b", "ctrl+u":
		m.moveList(-m.listPageSize())
	case "pgdown", "ctrl+f", "ctrl+d":
		m.moveList(m.listPageSize())
	case "home", "g":
		m.moveList(-len(m.view))
	case "end", "G":
		m.moveList(len(m.view))
	case "enter":
		m.focus = paneDetail
	}
}

func (m *Model) treeIndex(rows []*node) int {
	for i, n := range rows {
		if n == m.treeSel {
			return i
		}
	}
	return min(m.treeCursor, len(rows)-1)
}

func (m *Model) treeKey(k string) {
	rows := m.tree.visible()
	if len(rows) == 0 {
		return
	}
	i := max(m.treeIndex(rows), 0)
	n := rows[i]
	switch k {
	case "up", "k":
		i--
	case "down", "j":
		i++
	case "pgup", "ctrl+u":
		i -= m.listPageSize()
	case "pgdown", "ctrl+d":
		i += m.listPageSize()
	case "home", "g":
		i = 0
	case "end", "G":
		i = len(rows) - 1
	case "right", "l":
		if n.hasChildren() && !n.expanded {
			m.tree.setExpanded(n, true)
		} else if n.hasChildren() {
			i++
		}
	case "left", "h":
		if n.hasChildren() && n.expanded {
			m.tree.setExpanded(n, false)
		} else if n.parent != nil && n.parent != m.tree.root {
			m.treeSel = n.parent
			m.tree.setExpanded(n.parent, false)
			return
		}
	case "enter":
		t := n.filterTopic()
		if m.flt.text == t {
			m.setFilter("")
		} else {
			m.setFilter(t)
		}
	case "e":
		m.tree.setAll(true)
	case "E":
		m.tree.setAll(false)
		for n.parent != nil && n.parent != m.tree.root {
			n = n.parent
		}
		m.treeSel = n
		return
	}
	rows = m.tree.visible()
	i = max(min(i, len(rows)-1), 0)
	m.treeSel = rows[i]
	m.treeCursor = i
}

func (m *Model) handleMouse(msg tea.MouseMsg) {
	treeW, listH, _ := m.sizes()
	var target pane
	switch {
	case msg.X < treeW:
		target = paneTree
	case msg.Y < listH:
		target = paneList
	default:
		target = paneDetail
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		if msg.Action != tea.MouseActionPress {
			return
		}
		d := 3
		if msg.Button == tea.MouseButtonWheelUp {
			d = -3
		}
		switch target {
		case paneTree:
			rows := m.tree.visible()
			if len(rows) > 0 {
				i := max(min(m.treeIndex(rows)+d, len(rows)-1), 0)
				m.treeSel, m.treeCursor = rows[i], i
			}
		case paneList:
			m.moveList(d)
		case paneDetail:
			if d < 0 {
				m.detail.ScrollUp(-d)
			} else {
				m.detail.ScrollDown(d)
			}
		}
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return
		}
		m.focus = target
		row := msg.Y - 1 // inside the top border
		switch target {
		case paneTree:
			rows := m.tree.visible()
			if i := m.treeOffset + row; row >= 0 && i < len(rows) {
				if rows[i] == m.treeSel && rows[i].hasChildren() {
					m.tree.setExpanded(rows[i], !rows[i].expanded)
				}
				m.treeSel, m.treeCursor = rows[i], i
			}
		case paneList:
			if i := m.offset + row; row >= 0 && i < len(m.view) {
				m.moveList(i - m.cursor)
			}
		}
	}
}

// --- view ---

// sizes returns the tree width (0 when hidden) and the heights of the list
// and detail panes.
func (m *Model) sizes() (treeW, listH, detailH int) {
	treeW = m.treeWidth()
	body := m.h - 1
	listH = body * 55 / 100
	if listH < 4 {
		listH = min(body, 4)
	}
	detailH = body - listH
	if detailH < 3 {
		listH += detailH
		detailH = 0
	}
	return
}

func (m *Model) treeWidth() int {
	if !m.showTree || m.w < 70 {
		return 0
	}
	return max(min(m.w*30/100, 56), 26)
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	if m.w < 30 || m.h < 8 {
		return "Terminal too small"
	}
	treeW, listH, detailH := m.sizes()
	rightW := m.w - treeW

	right := m.renderList(rightW, listH)
	if detailH > 0 {
		right += "\n" + m.renderDetail(rightW, detailH)
	}
	body := right
	if treeW > 0 {
		left := strings.Split(m.renderTree(treeW, listH+detailH), "\n")
		rl := strings.Split(right, "\n")
		for i := range rl {
			if i < len(left) {
				rl[i] = left[i] + rl[i]
			}
		}
		body = strings.Join(rl, "\n")
	}
	if m.dialog != nil {
		body = center(body, m.dialog.View(m), m.w, m.h-1)
	}
	return body + "\n" + m.renderStatus()
}

func (m *Model) renderList(w, h int) string {
	rows := h - 2
	inner := w - 2
	m.offset = windowStart(m.offset, m.cursor, rows, len(m.view))
	if m.follow {
		m.offset = max(len(m.view)-rows, 0)
	}

	// Size the topic column to the visible topics, up to 45% of the width.
	topicW := 8
	end := min(m.offset+rows, len(m.view))
	for _, e := range m.view[m.offset:end] {
		topicW = max(topicW, ansi.StringWidth(e.Topic))
	}
	topicW = min(topicW, inner*45/100)

	var lines []string
	for i := m.offset; i < end; i++ {
		e := m.view[i]
		ts := e.Time.Format("15:04:05.000")
		flags := fmt.Sprintf("%d", e.QoS)
		if e.Retained {
			flags = "R" + flags
		} else {
			flags = " " + flags
		}
		topic := fit(e.Topic, topicW)
		payW := max(inner-12-1-2-1-topicW-2, 0)
		pay := preview(e.Payload, payW+1)
		if i == m.cursor {
			line := fmt.Sprintf("%s %s %s  %s", ts, flags, topic, pay)
			st := stSelected
			if m.focus == paneList {
				st = st.Bold(true)
			}
			lines = append(lines, st.Render(fit(line, inner)))
			continue
		}
		lines = append(lines, stDim.Render(ts)+" "+stMuted.Render(flags)+" "+stTopic.Render(topic)+"  "+stText.Render(ansi.Truncate(pay, payW, "…")))
	}
	if len(m.view) == 0 {
		lines = append(lines, "", "  "+stDim.Render(m.emptyListText()))
	}

	note := formatCount(len(m.view))
	if m.flt.active() || len(m.view) != len(m.store.msgs) {
		note += "/" + formatCount(len(m.store.msgs))
	}
	if m.paused {
		note = stWarn.Render(fmt.Sprintf("PAUSED +%d", m.pausedPending)) + stMuted.Render(" · "+note)
	} else if !m.follow {
		note += " · ↓G latest"
	}
	title := "Messages"
	if m.flt.active() {
		title += " ⧩ " + m.flt.text
	}
	return box(title, note, lines, w, h, m.focus == paneList)
}

func (m *Model) emptyListText() string {
	switch {
	case m.flt.active() && len(m.store.msgs) > 0:
		return "No messages match the filter (esc to clear)"
	case len(m.subs) == 0:
		return "Not subscribed to anything — press s to subscribe"
	case m.state != mqttx.StateConnected:
		return "Waiting for the connection…"
	default:
		var t []string
		for _, s := range m.subs {
			t = append(t, s.Topic)
		}
		return "Waiting for messages on " + strings.Join(t, ", ") + "…"
	}
}

func (m *Model) renderTree(w, h int) string {
	inner := w - 2
	visible := h - 2
	// New branches open automatically while everything still fits.
	m.tree.autoExpand = visible
	rows := m.tree.visible()
	if m.treeSel == nil && len(rows) > 0 {
		m.treeSel = rows[0]
	}
	cur := m.treeIndex(rows)
	if cur >= 0 {
		m.treeCursor = cur
	}
	m.treeOffset = windowStart(m.treeOffset, max(cur, 0), visible, len(rows))
	now := time.Now()

	var lines []string
	for i := m.treeOffset; i < min(m.treeOffset+visible, len(rows)); i++ {
		n := rows[i]
		marker := "  "
		if n.hasChildren() {
			marker = "▸ "
			if n.expanded {
				marker = "▾ "
			}
		}
		name := n.name
		if name == "" {
			name = "(empty)"
		}
		indent := strings.Repeat("  ", n.depth)
		head := indent + marker + name
		extra := ""
		if n.hasChildren() {
			extra = fmt.Sprintf(" (%s)", formatCount(n.total))
		}
		value := ""
		if n.count > 0 && n.last != nil {
			value = preview(n.last.Payload, inner)
		}
		if i == cur {
			st := stSelected
			if m.focus == paneTree {
				st = st.Bold(true)
			}
			line := head + extra
			if value != "" {
				line += "  " + value
			}
			lines = append(lines, st.Render(fit(line, inner)))
			continue
		}
		line := stDim.Render(indent+marker) + stTopic.Render(name) + stDim.Render(extra)
		if value != "" {
			vs := stMuted
			if n.last != nil && now.Sub(n.last.Time) < 2*time.Second {
				vs = stFresh
			}
			rest := inner - ansi.StringWidth(head+extra) - 2
			if rest > 1 {
				line += "  " + vs.Render(ansi.Truncate(value, rest, "…"))
			}
		}
		lines = append(lines, line)
	}
	if len(rows) == 0 {
		lines = append(lines, "", "  "+stDim.Render("No topics yet"))
	}
	return box("Topics", formatCount(m.tree.topics), lines, w, h, m.focus == paneTree)
}

func (m *Model) renderDetail(w, h int) string {
	e := m.current()
	inner := w - 2
	m.detail.Width = inner
	m.detail.Height = h - 2
	if e != m.detailFor || inner != m.detailW || e == nil {
		if e != m.detailFor {
			m.detail.GotoTop()
		}
		m.detailFor = e
		m.detailW = inner
		m.detail.SetContent(m.detailContent(e, inner))
	}
	note := ""
	if e != nil {
		note = humanBytes(len(e.Payload)) + " · view " + m.detailMode.String()
		if pct := m.detail.ScrollPercent(); m.detail.TotalLineCount() > m.detail.Height {
			note += fmt.Sprintf(" · %d%%", int(pct*100))
		}
	}
	return box("Message", note, strings.Split(m.detail.View(), "\n"), w, h, m.focus == paneDetail)
}

func (m *Model) detailContent(e *entry, width int) string {
	if e == nil {
		if m.focus == paneTree && m.treeSel != nil {
			return stDim.Render(" No messages on " + m.treeSel.full + " itself — open the branch to see its topics")
		}
		return stDim.Render(" Select a message to see it here")
	}
	var b strings.Builder
	b.WriteString(ansi.Hardwrap(stTopic.Bold(true).Render(e.Topic), width, true))
	b.WriteByte('\n')
	meta := []string{e.Time.Format("2006-01-02 15:04:05.000"), fmt.Sprintf("QoS %d", e.QoS)}
	if e.Retained {
		meta = append(meta, stWarn.Render("retained"))
	}
	body, kind := formatPayload(e.Payload, m.detailMode, width)
	meta = append(meta, kind)
	b.WriteString(stMuted.Render(strings.Join(meta, " · ")))
	b.WriteByte('\n')
	prop := func(k, v string) {
		b.WriteString(ansi.Hardwrap(stMuted.Render(k+": ")+v, width, true))
		b.WriteByte('\n')
	}
	if e.ContentType != "" {
		prop("content-type", e.ContentType)
	}
	if e.ResponseTopic != "" {
		prop("response-topic", e.ResponseTopic)
	}
	if len(e.CorrelationData) > 0 {
		cd := string(e.CorrelationData)
		if !isText(e.CorrelationData) {
			cd = fmt.Sprintf("%x", e.CorrelationData)
		}
		prop("correlation-data", cd)
	}
	for _, up := range e.UserProperties {
		prop(up[0], up[1])
	}
	b.WriteByte('\n')
	b.WriteString(ansi.Hardwrap(body, width, true))
	return b.String()
}

func (m *Model) renderStatus() string {
	if m.filtering {
		m.filterInput.Width = max(m.w-40, 10)
		line := stKey.Render(" / ") + m.filterInput.View()
		hint := hints("enter", "apply", "esc", "cancel")
		return barRender(fit(line, m.w-ansi.StringWidth(hint)-1) + hint + " ")
	}

	var state string
	switch m.state {
	case mqttx.StateConnected:
		state = stGood.Render("●") + " connected"
	case mqttx.StateConnecting:
		state = stWarn.Render("◌") + " connecting"
		if m.stateErr != nil {
			state = stWarn.Render("◌") + " reconnecting"
		}
	default:
		state = stBad.Render("○") + " disconnected"
	}

	var subs []string
	for _, s := range m.subs {
		subs = append(subs, s.Topic)
	}
	subText := "subs: none"
	if len(subs) > 0 {
		subText = "subs: " + strings.Join(subs, ", ")
	}
	ver := "v3.1.1"
	if m.cfg.Version == 5 {
		ver = "v5"
	}
	msgs := fmt.Sprintf("%s msgs · %.1f/s", formatCount(m.total), m.rate)

	// Segments in priority order; lower priority ones are dropped first.
	type seg struct {
		text string
		prio int
	}
	segs := []seg{
		{state, 0},
		{m.cfg.Broker, 1},
		{ver, 4},
		{"id: " + m.cfg.Client.ClientID(), 5},
		{ansi.Truncate(subText, 50, "…"), 2},
		{msgs, 1},
	}
	if m.paused {
		segs = append(segs, seg{stWarn.Render("PAUSED"), 0})
	}

	right := hints("?", "help", "q", "quit")
	if m.flash != "" && time.Now().Before(m.flashUntil) {
		if m.flashErr {
			right = stBad.Render(m.flash)
		} else {
			right = stGood.Render(m.flash)
		}
	} else if m.state != mqttx.StateConnected && m.stateErr != nil {
		right = stBad.Render(m.stateErr.Error())
	}

	sep := stDim.Render(" │ ")
	render := func() string {
		var parts []string
		for _, s := range segs {
			parts = append(parts, s.text)
		}
		return " " + strings.Join(parts, sep)
	}
	maxRight := m.w / 2
	if ansi.StringWidth(right) > maxRight {
		right = ansi.Truncate(right, maxRight, "…")
	}
	for prio := 5; prio > 0; prio-- {
		if ansi.StringWidth(render())+ansi.StringWidth(right)+2 <= m.w {
			break
		}
		var kept []seg
		for _, s := range segs {
			if s.prio < prio {
				kept = append(kept, s)
			}
		}
		segs = kept
	}
	left := render()
	gap := m.w - ansi.StringWidth(left) - ansi.StringWidth(right) - 1
	if gap < 1 {
		return barRender(fit(left, m.w))
	}
	return barRender(left + strings.Repeat(" ", gap) + right + " ")
}

// barRender applies the status bar background across a line whose segments
// carry their own styles (each of which ends with a full reset).
func barRender(s string) string {
	probe := stBar.Render("\x00")
	i := strings.IndexByte(probe, 0)
	if i <= 0 {
		return s
	}
	prefix := probe[:i]
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+prefix)
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+prefix)
	return prefix + s + "\x1b[0m"
}
