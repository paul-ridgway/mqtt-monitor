package app

import (
	"testing"

	"github.com/paul-ridgway/mqtt-monitor/internal/config"
)

func TestParseArgs(t *testing.T) {
	o, _, ok := parseArgs([]string{"mqmon", "-t", "a/#", "broker.local", "--topic", "b", "-V", "5", "-u", "me"})
	if !ok {
		t.Fatal("parse failed")
	}
	if o.positional != "broker.local" {
		t.Errorf("positional = %q", o.positional)
	}
	if len(o.topics) != 2 || o.topics[0] != "a/#" || o.topics[1] != "b" {
		t.Errorf("topics = %v", o.topics)
	}
	if o.version != 5 || !o.set["mqtt-version"] || !o.set["username"] || !o.set["topic"] {
		t.Errorf("version=%d set=%v", o.version, o.set)
	}

	if _, code, ok := parseArgs([]string{"mqmon", "-V", "4"}); !ok || code != 0 {
		t.Errorf("-V 4 should be accepted as 3.1.1")
	}
	if _, code, ok := parseArgs([]string{"mqmon", "-V", "7"}); ok || code != 2 {
		t.Errorf("-V 7 accepted")
	}
	if _, _, ok := parseArgs([]string{"mqmon", "a", "b"}); ok {
		t.Errorf("two positionals accepted")
	}
}

func TestResolveFromHostAndProfile(t *testing.T) {
	cfg := &config.File{}
	cfg.Put(&config.Profile{Name: "home", URL: "mqtt://broker:1883", Username: "me", Password: "pw", Version: 5})

	o, _, _ := parseArgs([]string{"mqmon", "home"})
	p, pw, err := resolve(o, cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "home" || p.Version != 5 || pw != "pw" {
		t.Errorf("profile %+v pw %q", p, pw)
	}

	// A host that matches a saved profile reuses it.
	o, _, _ = parseArgs([]string{"mqmon", "-u", "me", "broker"})
	p, _, err = resolve(o, cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "home" {
		t.Errorf("expected the saved profile, got %+v", p)
	}

	// Flags override the profile.
	o, _, _ = parseArgs([]string{"mqmon", "home", "-p", "1999", "-V", "3", "-i", "fixed"})
	p, _, _ = resolve(o, cfg, false)
	if p.URL != "mqtt://broker:1999" || p.Version != 3 || p.ClientID != "fixed" {
		t.Errorf("overrides not applied: %+v", p)
	}

	o, _, _ = parseArgs([]string{"mqmon", "--tls", "new.example"})
	p, _, _ = resolve(o, cfg, false)
	if p.URL != "mqtts://new.example:8883" || p.Name != "new.example:8883" {
		t.Errorf("new profile %+v", p)
	}

	o, _, _ = parseArgs([]string{"mqmon"})
	if _, _, err := resolve(o, cfg, false); err == nil {
		t.Errorf("expected an error with no broker and no terminal")
	}
}
