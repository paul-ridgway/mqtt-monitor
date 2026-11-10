package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/paul-ridgway/mqtt-monitor/internal/config"
	"github.com/paul-ridgway/mqtt-monitor/internal/mqttx"
)

const newConnection = "\x00new"

// resolve works out which broker to connect to, from the command line, a saved
// profile or by asking, and returns the profile to use and the password.
func resolve(o options, cfg *config.File, interactive bool) (*config.Profile, string, error) {
	var prof *config.Profile

	name := o.profile
	host := o.host
	if o.positional != "" {
		if name == "" && host == "" && cfg.Find(o.positional) != nil {
			name = o.positional
		} else if host == "" {
			host = o.positional
		} else {
			return nil, "", fmt.Errorf("unexpected argument %q (host already given)", o.positional)
		}
	}

	switch {
	case name != "":
		prof = cfg.Find(name)
		if prof == nil {
			return nil, "", fmt.Errorf("no profile called %q (see --list)", name)
		}
		prof = copyProfile(prof)
	case host != "":
		u, err := mqttx.ParseBroker(host, o.port, o.useTLS)
		if err != nil {
			return nil, "", err
		}
		if existing := cfg.FindURL(u.String(), o.username); existing != nil && o.saveAs == "" {
			prof = copyProfile(existing)
		} else {
			prof = &config.Profile{Name: u.Host, URL: u.String(), Version: o.version}
		}
	default:
		if !interactive {
			return nil, "", errors.New("no broker given (use --host, or run in a terminal to choose one)")
		}
		p, err := askProfile(o, cfg)
		if err != nil {
			return nil, "", err
		}
		prof = p
	}

	applyFlags(o, prof)
	if o.saveAs != "" {
		prof.Name = o.saveAs
	}

	password := o.password
	if password == "" && !o.set["password"] {
		password = os.Getenv("MQMON_PASSWORD")
	}
	if password == "" {
		password = prof.Password
	}
	if prof.Username != "" && password == "" && !o.set["password"] && interactive {
		err := huh.NewInput().
			Title(fmt.Sprintf("Password for %s on %s", prof.Username, prof.URL)).
			EchoMode(huh.EchoModePassword).
			Value(&password).
			Run()
		if err != nil {
			return nil, "", err
		}
	}
	if o.savePassword {
		prof.Password = password
	}
	return prof, password, nil
}

func copyProfile(p *config.Profile) *config.Profile {
	c := *p
	c.Topics = append([]config.Topic(nil), p.Topics...)
	c.Publishes = append([]config.Publish(nil), p.Publishes...)
	return &c
}

// applyFlags overrides profile settings with those given on the command line.
func applyFlags(o options, p *config.Profile) {
	if o.set["port"] || o.useTLS {
		if u, err := mqttx.ParseBroker(p.URL, o.port, o.useTLS); err == nil {
			p.URL = u.String()
		}
	}
	if o.set["username"] {
		p.Username = o.username
	}
	if o.set["client-id"] {
		p.ClientID = o.clientID
	}
	if o.set["mqtt-version"] || p.Version == 0 {
		p.Version = o.version
	}
	if o.set["cafile"] {
		p.CAFile = o.caFile
	}
	if o.set["cert"] {
		p.CertFile = o.certFile
	}
	if o.set["key"] {
		p.KeyFile = o.keyFile
	}
	if o.set["insecure"] {
		p.Insecure = o.insecure
	}
}

func askProfile(o options, cfg *config.File) (*config.Profile, error) {
	if recent := cfg.Recent(); len(recent) > 0 {
		choice := newConnection
		var opts []huh.Option[string]
		for _, p := range recent {
			label := p.Name
			detail := p.URL
			if p.Username != "" {
				detail = p.Username + " @ " + detail
			}
			if detail != p.Name {
				label += "  " + detail
			}
			opts = append(opts, huh.NewOption(label, p.Name))
		}
		opts = append(opts, huh.NewOption("New connection…", newConnection))
		choice = recent[0].Name
		err := huh.NewSelect[string]().
			Title("Connect to").
			Options(opts...).
			Value(&choice).
			Run()
		if err != nil {
			return nil, err
		}
		if choice != newConnection {
			return copyProfile(cfg.Find(choice)), nil
		}
	}
	return askNewProfile(o, cfg)
}

func askNewProfile(o options, cfg *config.File) (*config.Profile, error) {
	var (
		broker   = "localhost"
		username = o.username
		version  = o.version
		topics   = "#"
		clientID = o.clientID
		name     string
		caFile   = o.caFile
		certFile = o.certFile
		keyFile  = o.keyFile
		insecure = o.insecure
		advanced bool
	)
	if len(o.topics) > 0 {
		topics = strings.Join(o.topics, ", ")
	}
	secure := func() bool {
		u, err := mqttx.ParseBroker(broker, o.port, o.useTLS)
		return err == nil && (u.Scheme == "mqtts" || u.Scheme == "wss")
	}
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Broker").
				Description("host, host:port or URL (mqtt://, mqtts://, ws://, wss://)").
				Value(&broker).
				Validate(func(s string) error {
					_, err := mqttx.ParseBroker(s, o.port, o.useTLS)
					return err
				}),
			huh.NewInput().
				Title("Username").
				Description("leave empty to connect anonymously").
				Value(&username),
			huh.NewSelect[int]().
				Title("MQTT version").
				Options(huh.NewOption("3.1.1", 3), huh.NewOption("5", 5)).
				Value(&version),
			huh.NewInput().
				Title("Topics").
				Description("comma separated topic filters").
				Value(&topics).
				Validate(func(s string) error {
					for _, t := range splitTopics(s) {
						if err := mqttx.ValidFilter(t); err != nil {
							return fmt.Errorf("%s: %w", t, err)
						}
					}
					return nil
				}),
			huh.NewConfirm().
				Title("Set a client ID or TLS certificates?").
				Value(&advanced),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Client ID").
				Description("leave empty for mqmon-<hostname>-<pid>").
				Value(&clientID),
		).WithHideFunc(func() bool { return !advanced }),
		huh.NewGroup(
			huh.NewInput().Title("CA certificate file").Description("PEM, leave empty to use the system roots").Value(&caFile),
			huh.NewInput().Title("Client certificate file").Description("PEM, optional").Value(&certFile),
			huh.NewInput().Title("Client key file").Description("PEM, optional").Value(&keyFile),
			huh.NewConfirm().Title("Skip server certificate verification?").Value(&insecure),
		).WithHideFunc(func() bool { return !advanced || !secure() }),
	)
	if err := form.Run(); err != nil {
		return nil, err
	}

	u, err := mqttx.ParseBroker(broker, o.port, o.useTLS)
	if err != nil {
		return nil, err
	}
	name = u.Host
	if o.saveAs == "" && !o.noSave {
		err := huh.NewInput().
			Title("Save as").
			Description("run it again later with: mqmon <name>").
			Value(&name).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("a name is needed")
				}
				return nil
			}).
			Run()
		if err != nil {
			return nil, err
		}
	}
	p := &config.Profile{
		Name:     strings.TrimSpace(name),
		URL:      u.String(),
		Username: strings.TrimSpace(username),
		ClientID: strings.TrimSpace(clientID),
		Version:  version,
		CAFile:   caFile,
		CertFile: certFile,
		KeyFile:  keyFile,
		Insecure: insecure,
	}
	for _, t := range splitTopics(topics) {
		p.Topics = append(p.Topics, config.Topic{Topic: t, QoS: byte(o.qos)})
	}
	if existing := cfg.Find(p.Name); existing != nil {
		p.Publishes = existing.Publishes
	}
	// The version picked in the form should win over the flag default.
	o.set["mqtt-version"] = false
	return p, nil
}

func splitTopics(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}
