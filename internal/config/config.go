// Package config stores saved broker profiles and history in the user's
// config directory.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Topic is a saved subscription.
type Topic struct {
	Topic string `json:"topic"`
	QoS   byte   `json:"qos"`
}

// Publish is a remembered publish, offered again in the publish dialog.
type Publish struct {
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
	QoS     byte   `json:"qos"`
	Retain  bool   `json:"retain"`
}

// Profile is a saved broker connection.
type Profile struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Username  string    `json:"username,omitempty"`
	Password  string    `json:"password,omitempty"` // only stored when asked to
	ClientID  string    `json:"client_id,omitempty"`
	Version   int       `json:"version,omitempty"`
	CAFile    string    `json:"ca_file,omitempty"`
	CertFile  string    `json:"cert_file,omitempty"`
	KeyFile   string    `json:"key_file,omitempty"`
	Insecure  bool      `json:"insecure,omitempty"`
	Topics    []Topic   `json:"topics,omitempty"`
	Publishes []Publish `json:"publishes,omitempty"`
	LastUsed  time.Time `json:"last_used"`
}

// MaxPublishes is how many recent publishes are kept per profile.
const MaxPublishes = 20

// File is the on-disk config.
type File struct {
	Profiles []*Profile `json:"profiles"`

	path string
}

// DefaultPath returns the config file location, honouring $MQMON_CONFIG.
func DefaultPath() (string, error) {
	if p := os.Getenv("MQMON_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mqtt-monitor", "config.json"), nil
}

// Load reads the config, returning an empty one if it does not exist yet.
func Load(path string) (*File, error) {
	f := &File{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return f, nil
}

// Path is where the config is saved.
func (f *File) Path() string { return f.path }

// Save writes the config, readable only by the user since it may hold
// passwords.
func (f *File) Save() error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.path)
}

// SaveProfile re-reads the file, so changes made by other running instances
// are kept, then stores p and saves.
func SaveProfile(path string, p *Profile) error {
	f, err := Load(path)
	if err != nil {
		return err
	}
	f.Put(p)
	return f.Save()
}

// Find returns the profile with the given name, or nil.
func (f *File) Find(name string) *Profile {
	for _, p := range f.Profiles {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// FindURL returns the most recently used profile for the given broker URL and
// username, or nil.
func (f *File) FindURL(url, username string) *Profile {
	for _, p := range f.Recent() {
		if p.URL == url && p.Username == username {
			return p
		}
	}
	return nil
}

// Recent returns the profiles, most recently used first.
func (f *File) Recent() []*Profile {
	out := append([]*Profile(nil), f.Profiles...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastUsed.After(out[j].LastUsed) })
	return out
}

// Put adds the profile, replacing any existing one with the same name.
func (f *File) Put(p *Profile) {
	for i, existing := range f.Profiles {
		if existing.Name == p.Name {
			f.Profiles[i] = p
			return
		}
	}
	f.Profiles = append(f.Profiles, p)
}

// Delete removes the named profile.
func (f *File) Delete(name string) bool {
	for i, p := range f.Profiles {
		if p.Name == name {
			f.Profiles = append(f.Profiles[:i], f.Profiles[i+1:]...)
			return true
		}
	}
	return false
}

// RememberPublish records a publish at the front of the profile's history,
// dropping duplicates and old entries.
func (p *Profile) RememberPublish(pub Publish) {
	out := []Publish{pub}
	for _, existing := range p.Publishes {
		if existing != pub {
			out = append(out, existing)
		}
	}
	if len(out) > MaxPublishes {
		out = out[:MaxPublishes]
	}
	p.Publishes = out
}
