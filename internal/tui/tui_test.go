package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/paul-ridgway/mqtt-monitor/internal/config"
	"github.com/paul-ridgway/mqtt-monitor/internal/mqttx"
)

type fakeClient struct {
	subs      []mqttx.Subscription
	published []mqttx.PublishOptions
}

func (f *fakeClient) Subscribe(topic string, qos byte) error {
	f.subs = append(f.subs, mqttx.Subscription{Topic: topic, QoS: qos})
	return nil
}

func (f *fakeClient) Unsubscribe(topic string) error {
	for i, s := range f.subs {
		if s.Topic == topic {
			f.subs = append(f.subs[:i], f.subs[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("not subscribed")
}

func (f *fakeClient) Publish(p mqttx.PublishOptions) error {
	f.published = append(f.published, p)
	return nil
}

func (f *fakeClient) Subscriptions() []mqttx.Subscription { return f.subs }
func (f *fakeClient) ClientID() string                    { return "test-client" }
func (f *fakeClient) Close()                              {}

func msg(topic, payload string) *mqttx.Message {
	return &mqttx.Message{Topic: topic, Payload: []byte(payload), Time: time.Now()}
}

func newTestModel(t *testing.T, buffer int) (*Model, *fakeClient) {
	t.Helper()
	c := &fakeClient{subs: []mqttx.Subscription{{Topic: "#"}}}
	m := New(Config{Client: c, Feed: NewFeed(), Broker: "mqtt://test:1883", Version: 5, Profile: &config.Profile{Name: "t"}, Buffer: buffer})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m, c
}

func press(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeText(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestStoreTrimsAndViewFollows(t *testing.T) {
	m, _ := newTestModel(t, 200)
	for i := 0; i < 1000; i++ {
		m.Update(batchMsg{mqttx.MessageEvent{Message: msg(fmt.Sprintf("a/%d", i%5), fmt.Sprint(i))}})
	}
	if n := len(m.store.msgs); n > 220 || n < 200 {
		t.Fatalf("store holds %d messages", n)
	}
	if len(m.view) != len(m.store.msgs) {
		t.Fatalf("view %d != store %d", len(m.view), len(m.store.msgs))
	}
	if got := string(m.selected().Payload); got != "999" {
		t.Fatalf("selected %q, want the latest", got)
	}
	if m.tree.topics != 5 || m.total != 1000 {
		t.Fatalf("topics=%d total=%d", m.tree.topics, m.total)
	}
}

func TestFilter(t *testing.T) {
	cases := []struct {
		filter, topic, payload string
		want                   bool
	}{
		{"kitchen", "home/kitchen/temp", "1", true},
		{"KITCHEN", "home/kitchen/temp", "1", true},
		{"online", "z/bridge", `{"state":"online"}`, true},
		{"home/+/temp", "home/kitchen/temp", "", true},
		{"home/+/temp", "home/kitchen/humidity", "", false},
		{"home/#", "garden/x", "home/", false},
	}
	for _, c := range cases {
		e := &entry{Message: msg(c.topic, c.payload)}
		if got := newFilter(c.filter).match(e); got != c.want {
			t.Errorf("filter %q on %s %q = %v", c.filter, c.topic, c.payload, got)
		}
	}
}

func TestPauseHoldsMessages(t *testing.T) {
	m, _ := newTestModel(t, 0)
	m.Update(batchMsg{mqttx.MessageEvent{Message: msg("a", "1")}})
	m.Update(press("space"))
	m.Update(batchMsg{mqttx.MessageEvent{Message: msg("a", "2")}, mqttx.MessageEvent{Message: msg("a", "3")}})
	if len(m.view) != 1 || m.pausedPending != 2 {
		t.Fatalf("paused view=%d pending=%d", len(m.view), m.pausedPending)
	}
	m.Update(press("space"))
	if len(m.view) != 3 || m.pausedPending != 0 {
		t.Fatalf("resumed view=%d pending=%d", len(m.view), m.pausedPending)
	}
}

func TestTreeNavigationAndFilter(t *testing.T) {
	m, _ := newTestModel(t, 0)
	m.Update(batchMsg{
		mqttx.MessageEvent{Message: msg("home/kitchen/temp", "21")},
		mqttx.MessageEvent{Message: msg("home/hall/motion", "ON")},
		mqttx.MessageEvent{Message: msg("garden/temp", "12")},
	})
	m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyShiftTab}) // focus the tree
	if m.focus != paneTree {
		t.Fatalf("focus = %v", m.focus)
	}
	rows := m.tree.visible()
	if len(rows) != 2 || rows[0].name != "garden" {
		t.Fatalf("rows = %v", rows)
	}
	m.Update(press("down"))                  // home
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // expand
	if len(m.tree.visible()) != 4 {
		t.Fatalf("expanded rows = %d", len(m.tree.visible()))
	}
	m.Update(press("enter"))
	if m.flt.text != "home/#" || len(m.view) != 2 {
		t.Fatalf("filter %q, %d shown", m.flt.text, len(m.view))
	}
	// The detail pane follows the tree selection, and branches have no
	// message of their own.
	if m.current() != nil {
		t.Fatalf("branch should have no current message")
	}
	m.Update(press("esc"))
	if m.flt.active() {
		t.Fatalf("esc did not clear the filter")
	}
}

func TestPublishDialog(t *testing.T) {
	m, c := newTestModel(t, 0)
	m.Update(press("p"))
	if _, ok := m.dialog.(*publishDialog); !ok {
		t.Fatalf("dialog = %T", m.dialog)
	}
	typeText(m, "cmd/light")
	m.Update(press("tab"))
	typeText(m, "ON")
	_, cmd := m.Update(press("ctrl+s"))
	if m.dialog != nil {
		t.Fatalf("dialog still open")
	}
	res := cmd()
	if r, ok := res.(opResultMsg); !ok || r.err != nil {
		t.Fatalf("publish result %#v", res)
	}
	if len(c.published) != 1 || c.published[0].Topic != "cmd/light" || string(c.published[0].Payload) != "ON" {
		t.Fatalf("published %+v", c.published)
	}
	if len(m.cfg.Profile.Publishes) != 1 {
		t.Fatalf("publish not remembered")
	}
}

func TestSubscribeReplace(t *testing.T) {
	m, c := newTestModel(t, 0)
	m.Update(press("s"))
	typeText(m, "home/#")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m.Update(cmd())
	if len(c.subs) != 1 || c.subs[0].Topic != "home/#" {
		t.Fatalf("subs = %+v", c.subs)
	}
	if len(m.subs) != 1 {
		t.Fatalf("model subs not refreshed")
	}
}

func TestViewFitsAllSizes(t *testing.T) {
	m, _ := newTestModel(t, 0)
	m.Update(batchMsg{
		mqttx.MessageEvent{Message: msg("home/kitchen/temp", "21")},
		mqttx.MessageEvent{Message: msg("zigbee2mqtt/bridge/state", `{"state":"online","devices":[1,2,3]}`)},
		mqttx.MessageEvent{Message: &mqttx.Message{Topic: "bin", Payload: []byte{0, 1, 2, 255}, Time: time.Now()}},
		mqttx.StateEvent{State: mqttx.StateConnected},
	})
	dialogs := []string{"", "?", "s", "p", "D"}
	for _, size := range [][2]int{{30, 8}, {60, 15}, {80, 24}, {200, 60}} {
		for _, d := range dialogs {
			m.dialog = nil
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			if d != "" {
				m.Update(press(d))
			}
			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) != size[1] {
				t.Errorf("%dx%d dialog %q: %d lines", size[0], size[1], d, len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%dx%d dialog %q: line %d is %d wide", size[0], size[1], d, i, w)
				}
			}
		}
	}
}

func TestHexDumpWidth(t *testing.T) {
	data := []byte(strings.Repeat("0123456789abcdef", 4))
	for _, w := range []int{30, 50, 80} {
		for _, l := range strings.Split(hexDump(data, w), "\n") {
			if lw := ansi.StringWidth(l); lw > w && w >= 30 {
				t.Errorf("width %d: line %q is %d wide", w, ansi.Strip(l), lw)
			}
		}
	}
}

func TestPreview(t *testing.T) {
	if got := preview([]byte("a\n  b\tc"), 100); got != "a b c" {
		t.Errorf("preview collapsed whitespace = %q", got)
	}
	if got := preview([]byte{0xff, 0}, 100); !strings.HasPrefix(got, "<binary") {
		t.Errorf("binary preview = %q", got)
	}
	if got := preview(nil, 10); got != "(empty)" {
		t.Errorf("empty preview = %q", got)
	}
}

func TestFormatCount(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567"} {
		if got := formatCount(in); got != want {
			t.Errorf("formatCount(%d) = %s", in, got)
		}
	}
}
