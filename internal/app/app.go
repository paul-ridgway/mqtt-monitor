// Package app implements the mqtt-monitor command line.
package app

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	"github.com/paul-ridgway/mqtt-monitor/internal/config"
	"github.com/paul-ridgway/mqtt-monitor/internal/mqttx"
	"github.com/paul-ridgway/mqtt-monitor/internal/tui"
)

// Version is set by release builds via -ldflags.
var Version = "dev"

const usage = `Monitor an MQTT broker: watch topics, change subscriptions and publish.

Usage:
  %[1]s [options] [profile | host[:port] | url]

With no broker given, %[1]s offers your saved connections or asks for one.
URLs may use mqtt://, mqtts://, ws:// or wss://.

Connection:
  -h, --host <host>          Broker host, host:port or URL
  -p, --port <port>          Broker port (default 1883, 8883 with TLS)
  -u, --username <user>      Username
  -P, --password <pass>      Password (or $MQMON_PASSWORD; asked for when a
                             username is given without one)
  -i, --client-id <id>       Client ID (default mqmon-<hostname>-<pid>)
  -V, --mqtt-version <3|5>   MQTT protocol version (default 3, i.e. 3.1.1)
  -k, --keepalive <secs>     Keep-alive interval (default 30)
      --tls                  Use TLS (mqtts) even if the URL does not say so
      --cafile <file>        CA certificate(s) to trust (PEM)
      --cert <file>          Client certificate (PEM)
      --key <file>           Client private key (PEM)
      --insecure             Do not verify the server certificate

Subscriptions:
  -t, --topic <filter>       Topic to subscribe to; repeat for more
                             (default: the profile's last topics, or #)
  -q, --qos <0|1|2>          QoS for -t subscriptions (default 0)

Profiles (saved in %[2]s):
  -c, --profile <name>       Use a saved profile
      --save <name>          Save this connection under a name (default: the
                             host); later runs can use "%[1]s <name>"
      --save-password        Store the password in the profile (plain text,
                             file readable only by you)
      --no-save              Do not record this connection
      --list                 List saved profiles and exit
      --forget <name>        Delete a saved profile and exit

Output:
      --plain                Print "topic payload" lines instead of the full
                             screen interface (automatic when piped)
      --timestamps           With --plain, prefix each line with the time
      --log <file>           Append every message to a JSON lines file
      --buffer <n>           Messages kept in memory (default 10000)

  -v, --version              Show the version
      --help                 Show this help

Keys (in the interface): s subscribe, p publish, / filter, space pause,
? for everything else.
`

type topicList []string

func (t *topicList) String() string     { return strings.Join(*t, ",") }
func (t *topicList) Set(v string) error { *t = append(*t, v); return nil }

type options struct {
	host, username, password, clientID string
	port, version, keepalive, qos      int
	useTLS, insecure                   bool
	caFile, certFile, keyFile          string
	topics                             topicList
	profile, saveAs                    string
	savePassword, noSave, list         bool
	forget                             string
	plain, timestamps                  bool
	logFile                            string
	buffer                             int
	showVersion                        bool
	positional                         string
	set                                map[string]bool
}

func parseArgs(args []string) (options, int, bool) {
	o := options{set: map[string]bool{}}
	name := filepath.Base(args[0])
	cfgPath, _ := config.DefaultPath()
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintf(fs.Output(), usage, name, cfgPath) }

	str := func(p *string, names ...string) {
		for _, n := range names {
			fs.StringVar(p, n, *p, "")
		}
	}
	num := func(p *int, def int, names ...string) {
		*p = def
		for _, n := range names {
			fs.IntVar(p, n, def, "")
		}
	}
	boolean := func(p *bool, names ...string) {
		for _, n := range names {
			fs.BoolVar(p, n, false, "")
		}
	}
	str(&o.host, "host", "h")
	num(&o.port, 0, "port", "p")
	str(&o.username, "username", "u")
	str(&o.password, "password", "P")
	str(&o.clientID, "client-id", "i")
	num(&o.version, 3, "mqtt-version", "V")
	num(&o.keepalive, 30, "keepalive", "k")
	boolean(&o.useTLS, "tls")
	str(&o.caFile, "cafile")
	str(&o.certFile, "cert")
	str(&o.keyFile, "key")
	boolean(&o.insecure, "insecure")
	fs.Var(&o.topics, "topic", "")
	fs.Var(&o.topics, "t", "")
	num(&o.qos, 0, "qos", "q")
	str(&o.profile, "profile", "c")
	str(&o.saveAs, "save")
	boolean(&o.savePassword, "save-password")
	boolean(&o.noSave, "no-save")
	boolean(&o.list, "list")
	str(&o.forget, "forget")
	boolean(&o.plain, "plain")
	boolean(&o.timestamps, "timestamps")
	str(&o.logFile, "log")
	num(&o.buffer, 10000, "buffer")
	boolean(&o.showVersion, "version", "v")

	// Allow flags before and after the positional argument.
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return o, 0, false
			}
			return o, 2, false
		}
		if fs.NArg() == 0 {
			break
		}
		if o.positional != "" {
			fmt.Fprintf(fs.Output(), "unexpected argument: %s\n", fs.Arg(0))
			return o, 2, false
		}
		o.positional = fs.Arg(0)
		rest = fs.Args()[1:]
	}
	fs.Visit(func(f *flag.Flag) { o.set[f.Name] = true })
	alias := map[string]string{"h": "host", "p": "port", "u": "username", "P": "password", "i": "client-id", "V": "mqtt-version", "k": "keepalive", "t": "topic", "q": "qos", "c": "profile"}
	for short, long := range alias {
		if o.set[short] {
			o.set[long] = true
		}
	}
	if o.version != 3 && o.version != 4 && o.version != 5 {
		fmt.Fprintln(fs.Output(), "--mqtt-version must be 3 or 5")
		return o, 2, false
	}
	if o.version == 4 {
		o.version = 3
	}
	if o.qos < 0 || o.qos > 2 {
		fmt.Fprintln(fs.Output(), "--qos must be 0, 1 or 2")
		return o, 2, false
	}
	return o, 0, true
}

// Run runs the command and returns the exit code.
func Run(args []string) int {
	o, code, ok := parseArgs(args)
	if !ok {
		return code
	}
	if o.showVersion {
		fmt.Println("mqtt-monitor " + versionString())
		return 0
	}
	if err := run(o); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return 130
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func versionString() string {
	if Version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return Version
}

func run(o options) error {
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	switch {
	case o.list:
		return listProfiles(cfg)
	case o.forget != "":
		if !cfg.Delete(o.forget) {
			return fmt.Errorf("no profile called %q", o.forget)
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("Forgot %s\n", o.forget)
		return nil
	}

	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	plain := o.plain || !term.IsTerminal(int(os.Stdout.Fd()))

	prof, password, err := resolve(o, cfg, interactive)
	if err != nil {
		return err
	}

	u, err := mqttx.ParseBroker(prof.URL, 0, false)
	if err != nil {
		return err
	}
	var subs []mqttx.Subscription
	if len(o.topics) > 0 {
		for _, t := range o.topics {
			subs = append(subs, mqttx.Subscription{Topic: t, QoS: byte(o.qos)})
		}
	} else {
		for _, t := range prof.Topics {
			subs = append(subs, mqttx.Subscription{Topic: t.Topic, QoS: t.QoS})
		}
	}
	if len(subs) == 0 && !o.set["topic"] {
		subs = []mqttx.Subscription{{Topic: "#"}}
	}
	prof.Topics = nil
	for _, s := range subs {
		prof.Topics = append(prof.Topics, config.Topic{Topic: s.Topic, QoS: s.QoS})
	}
	for _, s := range subs {
		if err := mqttx.ValidFilter(s.Topic); err != nil {
			return fmt.Errorf("topic %q: %w", s.Topic, err)
		}
	}

	opts := mqttx.Options{
		URL:       u,
		Username:  prof.Username,
		Password:  password,
		ClientID:  prof.ClientID,
		Version:   prof.Version,
		KeepAlive: time.Duration(o.keepalive) * time.Second,
		TLS: mqttx.TLSOptions{
			CAFile:   prof.CAFile,
			CertFile: prof.CertFile,
			KeyFile:  prof.KeyFile,
			Insecure: prof.Insecure,
		},
	}
	if opts.ClientID == "" {
		opts.ClientID = mqttx.DefaultClientID()
	}

	var logger *messageLog
	if o.logFile != "" {
		if logger, err = openLog(o.logFile); err != nil {
			return err
		}
		defer logger.Close()
	}

	save := func(c mqttx.Client) {
		if o.noSave {
			return
		}
		if c != nil {
			prof.Topics = nil
			for _, s := range c.Subscriptions() {
				prof.Topics = append(prof.Topics, config.Topic{Topic: s.Topic, QoS: s.QoS})
			}
		}
		prof.LastUsed = time.Now()
		if err := config.SaveProfile(cfg.Path(), prof); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not save profile:", err)
		}
	}

	if plain {
		return runPlain(opts, subs, logger, o.timestamps, func(c mqttx.Client) { save(c) })
	}

	feed := tui.NewFeed()
	emit := feed.Emit
	if logger != nil {
		emit = func(ev any) {
			if m, ok := ev.(mqttx.MessageEvent); ok {
				logger.Write(m.Message)
			}
			feed.Emit(ev)
		}
	}
	client, err := mqttx.Connect(opts, subs, emit)
	if err != nil {
		return err
	}
	// Record the connection straight away so it is offered next time even if
	// we exit abnormally.
	save(nil)
	err = tui.Run(tui.Config{
		Client:  client,
		Feed:    feed,
		Broker:  displayBroker(u, prof.Username),
		Version: prof.Version,
		Profile: prof,
		Buffer:  o.buffer,
	})
	client.Close()
	save(client)
	return err
}

func displayBroker(u interface{ String() string }, user string) string {
	s := u.String()
	if user != "" {
		if i := strings.Index(s, "://"); i >= 0 {
			s = s[:i+3] + user + "@" + s[i+3:]
		}
	}
	return s
}

func listProfiles(cfg *config.File) error {
	recent := cfg.Recent()
	if len(recent) == 0 {
		fmt.Println("No saved profiles. Connections are saved automatically when you use them.")
		return nil
	}
	for _, p := range recent {
		var topics []string
		for _, t := range p.Topics {
			topics = append(topics, t.Topic)
		}
		user := ""
		if p.Username != "" {
			user = " as " + p.Username
		}
		fmt.Printf("%-20s %s%s  v%s  [%s]  last used %s\n", p.Name, p.URL, user, versionName(p.Version), strings.Join(topics, ", "), p.LastUsed.Format("2006-01-02 15:04"))
	}
	fmt.Printf("\nSaved in %s\n", cfg.Path())
	return nil
}

func versionName(v int) string {
	if v == 5 {
		return "5"
	}
	return "3.1.1"
}

// waitForSignal blocks until SIGINT or SIGTERM.
func waitForSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	signal.Stop(ch)
}
