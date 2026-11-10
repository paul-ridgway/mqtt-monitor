package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Profiles) != 0 {
		t.Fatalf("expected empty config")
	}
	old := &Profile{Name: "old", URL: "mqtt://a:1883", LastUsed: time.Now().Add(-time.Hour)}
	newer := &Profile{Name: "new", URL: "mqtt://b:1883", LastUsed: time.Now(), Topics: []Topic{{"#", 0}}}
	f.Put(old)
	f.Put(newer)
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0600", info.Mode().Perm())
	}

	g, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	recent := g.Recent()
	if len(recent) != 2 || recent[0].Name != "new" {
		t.Fatalf("Recent() = %+v", recent)
	}
	if g.FindURL("mqtt://a:1883", "") == nil {
		t.Errorf("FindURL did not find profile")
	}
	if !g.Delete("old") || g.Find("old") != nil {
		t.Errorf("Delete failed")
	}
}

func TestRememberPublish(t *testing.T) {
	p := &Profile{}
	for i := 0; i < MaxPublishes+5; i++ {
		p.RememberPublish(Publish{Topic: "t", Payload: string(rune('a' + i%26))})
	}
	if len(p.Publishes) != MaxPublishes {
		t.Fatalf("kept %d publishes", len(p.Publishes))
	}
	p.RememberPublish(Publish{Topic: "x"})
	p.RememberPublish(Publish{Topic: "y"})
	p.RememberPublish(Publish{Topic: "x"})
	if p.Publishes[0].Topic != "x" || p.Publishes[1].Topic != "y" || p.Publishes[2].Topic == "x" {
		t.Errorf("publishes not de-duplicated: %+v", p.Publishes[:3])
	}
}
