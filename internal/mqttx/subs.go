package mqttx

import "sync"

// subSet is the ordered set of active subscriptions, shared between the
// caller and the reconnect handler.
type subSet struct {
	mu   sync.Mutex
	subs []Subscription
}

func newSubSet(initial []Subscription) *subSet {
	s := &subSet{}
	for _, sub := range initial {
		s.add(sub)
	}
	return s
}

func (s *subSet) add(sub Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.subs {
		if existing.Topic == sub.Topic {
			s.subs[i] = sub
			return
		}
	}
	s.subs = append(s.subs, sub)
}

func (s *subSet) remove(topic string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.subs {
		if existing.Topic == topic {
			s.subs = append(s.subs[:i], s.subs[i+1:]...)
			return true
		}
	}
	return false
}

func (s *subSet) list() []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Subscription(nil), s.subs...)
}
