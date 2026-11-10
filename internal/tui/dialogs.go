package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/paul-ridgway/mqtt-monitor/internal/config"
	"github.com/paul-ridgway/mqtt-monitor/internal/mqttx"
)

// dialog is a modal shown over the main view. Update returns nil to close.
type dialog interface {
	Update(m *Model, msg tea.KeyMsg) (dialog, tea.Cmd)
	View(m *Model) string
}

func dialogWidth(m *Model, want int) int {
	w := want
	if m.w-4 < w {
		w = m.w - 4
	}
	if w < 30 {
		w = 30
	}
	return w
}

func frame(title string, body []string, w int) string {
	return box(title, "", body, w, len(body)+2, true)
}

func newInput(placeholder string, suggestions []string) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = placeholder
	in.Cursor.SetMode(cursor.CursorStatic)
	in.ShowSuggestions = true
	in.SetSuggestions(suggestions)
	in.KeyMap.AcceptSuggestion = key.NewBinding(key.WithKeys("right"))
	in.KeyMap.NextSuggestion = key.NewBinding(key.WithKeys("ctrl+n"))
	in.KeyMap.PrevSuggestion = key.NewBinding(key.WithKeys("ctrl+p"))
	in.Focus()
	return in
}

// acceptSuggestion completes the input from its current suggestion, if any,
// reporting whether it did.
func acceptSuggestion(in *textinput.Model) bool {
	s := in.CurrentSuggestion()
	if s == "" || len(s) <= len(in.Value()) {
		return false
	}
	in.SetValue(s)
	in.CursorEnd()
	return true
}

func qosPicker(qos byte, focused bool) string {
	var parts []string
	for q := byte(0); q <= 2; q++ {
		label := fmt.Sprintf(" %d ", q)
		switch {
		case q == qos && focused:
			parts = append(parts, stSelected.Bold(true).Render(label))
		case q == qos:
			parts = append(parts, stBold.Render(label))
		default:
			parts = append(parts, stDim.Render(label))
		}
	}
	return strings.Join(parts, "")
}

func label(s string, focused bool) string {
	if focused {
		return stAccent.Bold(true).Render(fmt.Sprintf("%-8s", s))
	}
	return stMuted.Render(fmt.Sprintf("%-8s", s))
}

func hints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, stKey.Render(pairs[i])+" "+stDim.Render(pairs[i+1]))
	}
	return strings.Join(parts, stDim.Render("  ·  "))
}

func adjustQoS(qos byte, k string) (byte, bool) {
	switch k {
	case "left", "h":
		if qos > 0 {
			qos--
		}
	case "right", "l":
		if qos < 2 {
			qos++
		}
	case "0", "1", "2":
		qos = k[0] - '0'
	default:
		return qos, false
	}
	return qos, true
}

// --- subscriptions ---

type subscribeDialog struct {
	input  textinput.Model
	qos    byte
	focus  int // 0 topic, 1 qos, 2 list
	cursor int
	err    string
}

func newSubscribeDialog(m *Model, prefill string, focusList bool) *subscribeDialog {
	d := &subscribeDialog{input: newInput("topic filter, e.g. home/# or +/status", m.tree.suggestions())}
	d.input.SetValue(prefill)
	d.input.CursorEnd()
	if focusList && len(m.subs) > 0 {
		d.focus = 2
		d.input.Blur()
	}
	return d
}

func (d *subscribeDialog) setFocus(f int, n int) {
	if f < 0 {
		f = 2
	}
	if f > 2 || (f == 2 && n == 0) {
		f = 0
	}
	d.focus = f
	if f == 0 {
		d.input.Focus()
	} else {
		d.input.Blur()
	}
}

func (d *subscribeDialog) Update(m *Model, msg tea.KeyMsg) (dialog, tea.Cmd) {
	k := msg.String()
	d.err = ""
	if d.cursor >= len(m.subs) {
		d.cursor = len(m.subs) - 1
	}
	if d.cursor < 0 {
		d.cursor = 0
	}
	switch k {
	case "esc":
		return nil, nil
	case "tab":
		if d.focus == 0 && acceptSuggestion(&d.input) {
			return d, nil
		}
		d.setFocus(d.focus+1, len(m.subs))
		return d, nil
	case "shift+tab":
		d.setFocus(d.focus-1, len(m.subs))
		return d, nil
	case "enter", "ctrl+x":
		if d.focus == 2 {
			break
		}
		topic := strings.TrimSpace(d.input.Value())
		if err := mqttx.ValidFilter(topic); err != nil {
			d.err = err.Error()
			return d, nil
		}
		if k == "ctrl+x" {
			return nil, m.replaceCmd(topic, d.qos)
		}
		return nil, m.subscribeCmd(topic, d.qos)
	}

	switch d.focus {
	case 0:
		if k == "down" && len(m.subs) > 0 {
			d.setFocus(2, len(m.subs))
			return d, nil
		}
		var cmd tea.Cmd
		d.input, cmd = d.input.Update(msg)
		return d, cmd
	case 1:
		d.qos, _ = adjustQoS(d.qos, k)
	case 2:
		switch k {
		case "up", "k":
			if d.cursor == 0 {
				d.setFocus(0, len(m.subs))
			} else {
				d.cursor--
			}
		case "down", "j":
			if d.cursor < len(m.subs)-1 {
				d.cursor++
			}
		case "x", "d", "delete", "backspace":
			if len(m.subs) > 0 {
				return d, m.unsubscribeCmd(m.subs[d.cursor].Topic)
			}
		case "e", "enter":
			if len(m.subs) > 0 {
				s := m.subs[d.cursor]
				d.input.SetValue(s.Topic)
				d.input.CursorEnd()
				d.qos = s.QoS
				d.setFocus(0, len(m.subs))
			}
		}
	}
	return d, nil
}

func (d *subscribeDialog) View(m *Model) string {
	w := dialogWidth(m, 72)
	inner := w - 2
	d.input.Width = inner - 12
	body := []string{
		"",
		" " + label("Topic", d.focus == 0) + d.input.View(),
		" " + label("QoS", d.focus == 1) + qosPicker(d.qos, d.focus == 1),
		"",
		" " + stMuted.Render("Current subscriptions"),
	}
	if len(m.subs) == 0 {
		body = append(body, "   "+stDim.Render("none"))
	}
	for i, s := range m.subs {
		line := fmt.Sprintf("%s  %s", s.Topic, stDim.Render(fmt.Sprintf("QoS %d", s.QoS)))
		if d.focus == 2 && i == d.cursor {
			body = append(body, " "+stSelected.Render(fit(" ▸ "+s.Topic+fmt.Sprintf("  QoS %d", s.QoS), inner-2)))
		} else {
			body = append(body, "   "+line)
		}
	}
	body = append(body, "")
	if d.err != "" {
		body = append(body, " "+stBad.Render(d.err))
	}
	if d.focus == 2 {
		body = append(body, " "+hints("x", "unsubscribe", "e", "edit", "tab", "next", "esc", "close"))
	} else {
		body = append(body, " "+hints("enter", "add", "ctrl+x", "replace all", "→", "complete", "esc", "close"))
	}
	return frame("Subscriptions", body, w)
}

// --- publish ---

type publishDialog struct {
	topic   textinput.Model
	payload textarea.Model
	qos     byte
	retain  bool
	focus   int // 0 topic, 1 payload, 2 qos, 3 retain
	hist    []config.Publish
	histIdx int
	err     string
}

func newPublishDialog(m *Model, p config.Publish) *publishDialog {
	d := &publishDialog{topic: newInput("topic to publish to", m.publishSuggestions()), histIdx: -1}
	d.topic.SetValue(p.Topic)
	d.topic.CursorEnd()
	d.payload = textarea.New()
	d.payload.ShowLineNumbers = false
	d.payload.Prompt = ""
	d.payload.Placeholder = "payload"
	d.payload.Cursor.SetMode(cursor.CursorStatic)
	d.payload.CharLimit = 0
	d.payload.FocusedStyle.CursorLine = lipgloss.NewStyle()
	d.payload.SetValue(p.Payload)
	d.qos = p.QoS
	d.retain = p.Retain
	if m.cfg.Profile != nil {
		d.hist = m.cfg.Profile.Publishes
	}
	if p.Topic != "" {
		d.setFocus(1)
	}
	return d
}

func (d *publishDialog) setFocus(f int) {
	d.focus = (f + 4) % 4
	d.topic.Blur()
	d.payload.Blur()
	switch d.focus {
	case 0:
		d.topic.Focus()
	case 1:
		d.payload.Focus()
	}
}

func (d *publishDialog) load(p config.Publish) {
	d.topic.SetValue(p.Topic)
	d.topic.CursorEnd()
	d.payload.SetValue(p.Payload)
	d.qos = p.QoS
	d.retain = p.Retain
}

func (d *publishDialog) value() config.Publish {
	return config.Publish{Topic: strings.TrimSpace(d.topic.Value()), Payload: d.payload.Value(), QoS: d.qos, Retain: d.retain}
}

func (d *publishDialog) Update(m *Model, msg tea.KeyMsg) (dialog, tea.Cmd) {
	k := msg.String()
	d.err = ""
	switch k {
	case "esc":
		m.lastPublish = d.value()
		return nil, nil
	case "ctrl+s":
		return d.send(m)
	case "tab":
		if d.focus == 0 && acceptSuggestion(&d.topic) {
			return d, nil
		}
		d.setFocus(d.focus + 1)
		return d, nil
	case "shift+tab":
		d.setFocus(d.focus - 1)
		return d, nil
	}

	switch d.focus {
	case 0:
		switch k {
		case "enter":
			d.setFocus(1)
			return d, nil
		case "up", "down":
			if len(d.hist) == 0 {
				return d, nil
			}
			if k == "up" && d.histIdx < len(d.hist)-1 {
				d.histIdx++
			} else if k == "down" && d.histIdx > 0 {
				d.histIdx--
			}
			d.load(d.hist[d.histIdx])
			return d, nil
		}
		var cmd tea.Cmd
		d.topic, cmd = d.topic.Update(msg)
		return d, cmd
	case 1:
		if k == "ctrl+f" {
			var buf bytes.Buffer
			if err := json.Indent(&buf, []byte(strings.TrimSpace(d.payload.Value())), "", "  "); err != nil {
				d.err = "not valid JSON: " + err.Error()
			} else {
				d.payload.SetValue(buf.String())
			}
			return d, nil
		}
		var cmd tea.Cmd
		d.payload, cmd = d.payload.Update(msg)
		return d, cmd
	case 2:
		if k == "enter" {
			return d.send(m)
		}
		d.qos, _ = adjustQoS(d.qos, k)
	case 3:
		switch k {
		case "enter":
			return d.send(m)
		case " ", "left", "right", "x":
			d.retain = !d.retain
		case "y":
			d.retain = true
		case "n":
			d.retain = false
		}
	}
	return d, nil
}

func (d *publishDialog) send(m *Model) (dialog, tea.Cmd) {
	p := d.value()
	if err := mqttx.ValidTopic(p.Topic); err != nil {
		d.err = err.Error()
		d.setFocus(0)
		return d, nil
	}
	m.lastPublish = p
	return nil, m.publishCmd(p)
}

func (d *publishDialog) View(m *Model) string {
	w := dialogWidth(m, 90)
	inner := w - 2
	d.topic.Width = inner - 12
	ph := m.h / 3
	if ph < 3 {
		ph = 3
	}
	if ph > 14 {
		ph = 14
	}
	d.payload.SetWidth(inner - 10)
	d.payload.SetHeight(ph)

	body := []string{"", " " + label("Topic", d.focus == 0) + d.topic.View(), ""}
	for i, line := range strings.Split(d.payload.View(), "\n") {
		l := "        "
		if i == 0 {
			l = label("Payload", d.focus == 1)
		}
		body = append(body, " "+l+line)
	}
	check := ""
	if v := strings.TrimSpace(d.payload.Value()); strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
		if json.Valid([]byte(v)) {
			check = stGood.Render("✓ valid JSON")
		} else {
			check = stWarn.Render("✗ not valid JSON")
		}
	}
	size := stDim.Render(humanBytes(len(d.payload.Value())))
	body = append(body, " "+strings.Repeat(" ", 8)+size+"  "+check, "")
	box := "[ ]"
	if d.retain {
		box = "[x]"
	}
	if d.focus == 3 {
		box = stSelected.Bold(true).Render(box)
	}
	body = append(body, " "+label("QoS", d.focus == 2)+qosPicker(d.qos, d.focus == 2)+"     "+label("Retain", d.focus == 3)+box, "")
	if d.err != "" {
		body = append(body, " "+stBad.Render(d.err))
	}
	hist := ""
	if d.focus == 0 && len(d.hist) > 0 {
		hist = "↑↓"
	}
	pairs := []string{"ctrl+s", "send", "tab", "next field"}
	if hist != "" {
		pairs = append(pairs, hist, "history")
	}
	if d.focus == 1 {
		pairs = append(pairs, "ctrl+f", "format JSON")
	}
	pairs = append(pairs, "esc", "cancel")
	body = append(body, " "+hints(pairs...))
	title := "Publish"
	if m.state != mqttx.StateConnected {
		title += " (not connected)"
	}
	return frame(title, body, w)
}

// --- confirm ---

type confirmDialog struct {
	title, text string
	yes         tea.Cmd
}

func (d *confirmDialog) Update(_ *Model, msg tea.KeyMsg) (dialog, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return nil, d.yes
	case "n", "N", "esc", "q":
		return nil, nil
	}
	return d, nil
}

func (d *confirmDialog) View(m *Model) string {
	w := dialogWidth(m, 64)
	body := []string{""}
	for _, l := range strings.Split(d.text, "\n") {
		body = append(body, " "+l)
	}
	body = append(body, "", " "+hints("y", "yes", "n", "no"))
	return frame(d.title, body, w)
}

// --- help ---

type helpDialog struct{}

var helpSections = []struct {
	title string
	keys  [][2]string
}{
	{"General", [][2]string{
		{"tab / shift+tab", "move between panes"},
		{"s", "subscriptions: add, replace, remove"},
		{"u", "unsubscribe (opens the subscription list)"},
		{"p", "publish (topic from the current message)"},
		{"P", "edit and re-publish the current message"},
		{"D", "delete the retained message on the current topic"},
		{"/", "filter: text matches topic or payload, +/# match topics"},
		{"f", "filter to the current topic (again to clear)"},
		{"esc", "clear the filter"},
		{"space", "pause / resume the message list"},
		{"c", "clear messages and topics"},
		{"v", "payload view: auto / raw / hex"},
		{"y / Y", "copy payload / topic to the clipboard"},
		{"t", "show / hide the topic tree"},
		{"q, ctrl+c", "quit"},
	}},
	{"Messages", [][2]string{
		{"↑↓ j k, pgup pgdn", "move"},
		{"g / G, home / end", "first / latest (follows new messages)"},
		{"enter", "inspect in the detail pane"},
	}},
	{"Topics", [][2]string{
		{"↑↓ j k", "move"},
		{"→ l / ← h", "expand / collapse"},
		{"enter", "filter to this topic or branch"},
		{"e / E", "expand / collapse all"},
	}},
}

func (helpDialog) Update(_ *Model, msg tea.KeyMsg) (dialog, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "?", "enter":
		return nil, nil
	}
	return helpDialog{}, nil
}

func (helpDialog) View(m *Model) string {
	w := dialogWidth(m, 70)
	var body []string
	for _, s := range helpSections {
		body = append(body, "", " "+stBold.Render(s.title))
		for _, k := range s.keys {
			body = append(body, "   "+stKey.Render(fmt.Sprintf("%-20s", k[0]))+" "+k[1])
		}
	}
	body = append(body, "", " "+stDim.Render("Press esc to close"))
	if len(body)+2 > m.h {
		body = body[:max(m.h-2, 1)]
	}
	return frame("Help", body, w)
}
