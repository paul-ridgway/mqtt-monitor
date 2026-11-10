package tui

import (
	"sort"
	"strings"

	"github.com/paul-ridgway/mqtt-monitor/internal/mqttx"
)

// entry is a stored message with a sequence number.
type entry struct {
	*mqttx.Message
	seq int
}

// store keeps the most recent messages, oldest first.
type store struct {
	max  int
	msgs []*entry
	next int
}

func newStore(max int) *store {
	if max <= 0 {
		max = 10000
	}
	return &store{max: max}
}

// add appends a message and reports how many old messages were dropped.
func (s *store) add(m *mqttx.Message) (*entry, int) {
	e := &entry{Message: m, seq: s.next}
	s.next++
	s.msgs = append(s.msgs, e)
	// Trim in chunks so we are not copying on every message.
	if over := len(s.msgs) - s.max; over > s.max/10 || (over > 0 && s.max < 100) {
		s.msgs = append([]*entry(nil), s.msgs[over:]...)
		return e, over
	}
	return e, 0
}

func (s *store) oldestSeq() int {
	if len(s.msgs) == 0 {
		return s.next
	}
	return s.msgs[0].seq
}

func (s *store) clear() { s.msgs = nil }

// filter decides which messages are shown in the list.
type filter struct {
	text     string
	wildcard bool
	lower    string
}

func newFilter(text string) filter {
	text = strings.TrimSpace(text)
	return filter{
		text:     text,
		wildcard: strings.ContainsAny(text, "+#"),
		lower:    strings.ToLower(text),
	}
}

func (f filter) active() bool { return f.text != "" }

// match uses MQTT wildcard matching on the topic when the filter contains +
// or #, otherwise a case-insensitive substring match on topic or payload.
func (f filter) match(e *entry) bool {
	if f.text == "" {
		return true
	}
	if f.wildcard {
		return mqttx.Match(f.text, e.Topic)
	}
	if strings.Contains(strings.ToLower(e.Topic), f.lower) {
		return true
	}
	return len(e.Payload) < 65536 && strings.Contains(strings.ToLower(string(e.Payload)), f.lower)
}

// node is a level in the topic tree.
type node struct {
	name     string
	full     string
	depth    int
	parent   *node
	children map[string]*node
	sorted   []*node
	count    int // messages on exactly this topic
	total    int // messages on this topic and below
	last     *entry
	expanded bool
}

func (n *node) hasChildren() bool { return len(n.children) > 0 }

func (n *node) sortedChildren() []*node {
	if n.sorted == nil && len(n.children) > 0 {
		n.sorted = make([]*node, 0, len(n.children))
		for _, c := range n.children {
			n.sorted = append(n.sorted, c)
		}
		sort.Slice(n.sorted, func(i, j int) bool { return n.sorted[i].name < n.sorted[j].name })
	}
	return n.sorted
}

// filterTopic is the subscription/filter that selects this node: the topic
// itself for leaves, or topic/# for branches.
func (n *node) filterTopic() string {
	if n.hasChildren() {
		return n.full + "/#"
	}
	return n.full
}

// tree is the hierarchy of topics seen.
type tree struct {
	root   *node
	topics int
	rows   []*node
	dirty  bool
}

func newTree() *tree {
	return &tree{root: &node{children: map[string]*node{}, expanded: true, depth: -1}, dirty: true}
}

func (t *tree) add(e *entry) {
	n := t.root
	n.total++
	parts := strings.Split(e.Topic, "/")
	for i, p := range parts {
		c := n.children[p]
		if c == nil {
			c = &node{
				name:     p,
				full:     strings.Join(parts[:i+1], "/"),
				depth:    i,
				parent:   n,
				children: map[string]*node{},
			}
			n.children[p] = c
			n.sorted = nil
			if n.expanded {
				t.dirty = true
			}
		}
		c.total++
		n = c
	}
	if n.count == 0 {
		t.topics++
	}
	n.count++
	n.last = e
}

// visible returns the rows currently shown (expanded branches only).
func (t *tree) visible() []*node {
	if t.dirty {
		t.rows = t.rows[:0]
		var walk func(*node)
		walk = func(n *node) {
			for _, c := range n.sortedChildren() {
				t.rows = append(t.rows, c)
				if c.expanded {
					walk(c)
				}
			}
		}
		walk(t.root)
		t.dirty = false
	}
	return t.rows
}

func (t *tree) setExpanded(n *node, v bool) {
	if n.hasChildren() && n.expanded != v {
		n.expanded = v
		t.dirty = true
	}
}

func (t *tree) setAll(v bool) {
	var walk func(*node)
	walk = func(n *node) {
		for _, c := range n.children {
			if c.hasChildren() {
				c.expanded = v
			}
			walk(c)
		}
	}
	walk(t.root)
	t.dirty = true
}

// reveal expands the ancestors of n so it is visible.
func (t *tree) reveal(n *node) {
	for p := n.parent; p != nil && p != t.root; p = p.parent {
		t.setExpanded(p, true)
	}
}

func (t *tree) find(topic string) *node {
	n := t.root
	for _, p := range strings.Split(topic, "/") {
		n = n.children[p]
		if n == nil {
			return nil
		}
	}
	return n
}

// topicsList returns every topic seen, plus branch/# filters, for
// suggestions.
func (t *tree) suggestions() []string {
	var out []string
	var walk func(*node)
	walk = func(n *node) {
		for _, c := range n.sortedChildren() {
			if c.count > 0 {
				out = append(out, c.full)
			}
			if c.hasChildren() {
				out = append(out, c.full+"/#")
				walk(c)
			}
		}
	}
	walk(t.root)
	return out
}
