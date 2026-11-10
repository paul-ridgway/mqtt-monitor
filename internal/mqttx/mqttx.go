// Package mqttx wraps the Paho MQTT 3.1.1 and 5 clients behind a single
// interface that reports messages and connection changes as events.
package mqttx

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Message is a received (or published) MQTT message.
type Message struct {
	Topic    string
	Payload  []byte
	QoS      byte
	Retained bool
	Time     time.Time

	// MQTT 5 properties (empty for 3.1.1).
	ContentType     string
	ResponseTopic   string
	CorrelationData []byte
	UserProperties  [][2]string
}

// State is the connection state of a client.
type State int

const (
	StateConnecting State = iota
	StateConnected
	StateDisconnected
	StateClosed
)

func (s State) String() string {
	switch s {
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateDisconnected:
		return "disconnected"
	default:
		return "closed"
	}
}

// StateEvent is emitted whenever the connection state changes.
type StateEvent struct {
	State State
	Err   error
}

// MessageEvent is emitted for every message received.
type MessageEvent struct {
	Message *Message
}

// Options configures a connection.
type Options struct {
	URL       *url.URL // scheme mqtt, mqtts, ws or wss
	Username  string
	Password  string
	ClientID  string
	Version   int // 3 or 5
	KeepAlive time.Duration
	TLS       TLSOptions
}

// TLSOptions configures TLS for mqtts and wss connections.
type TLSOptions struct {
	CAFile   string
	CertFile string
	KeyFile  string
	Insecure bool
}

// Subscription is a topic filter and the QoS it was subscribed with.
type Subscription struct {
	Topic string
	QoS   byte
}

// PublishOptions configures an outgoing message.
type PublishOptions struct {
	Topic         string
	Payload       []byte
	QoS           byte
	Retain        bool
	ContentType   string // MQTT 5 only
	ResponseTopic string // MQTT 5 only
}

// Client is a connected (or reconnecting) MQTT client. Subscriptions are
// remembered and restored after a reconnect.
type Client interface {
	Subscribe(topic string, qos byte) error
	Unsubscribe(topic string) error
	Publish(p PublishOptions) error
	Subscriptions() []Subscription
	ClientID() string
	Close()
}

// Connect starts a client that connects in the background, reconnecting as
// needed. Events (StateEvent, MessageEvent) are passed to emit, which must not
// block for long.
func Connect(opts Options, initial []Subscription, emit func(any)) (Client, error) {
	if opts.URL == nil {
		return nil, errors.New("no broker URL")
	}
	if opts.ClientID == "" {
		opts.ClientID = DefaultClientID()
	}
	if opts.KeepAlive == 0 {
		opts.KeepAlive = 30 * time.Second
	}
	var tlsCfg *tls.Config
	if opts.URL.Scheme == "mqtts" || opts.URL.Scheme == "wss" {
		cfg, err := opts.TLS.Config(opts.URL.Hostname())
		if err != nil {
			return nil, err
		}
		tlsCfg = cfg
	}
	subs := newSubSet(initial)
	switch opts.Version {
	case 5:
		return connectV5(opts, tlsCfg, subs, emit)
	case 3, 4, 0:
		return connectV3(opts, tlsCfg, subs, emit)
	default:
		return nil, fmt.Errorf("unsupported MQTT version %d (use 3 or 5)", opts.Version)
	}
}

// Config builds a tls.Config from the options.
func (o TLSOptions) Config(serverName string) (*tls.Config, error) {
	cfg := &tls.Config{ServerName: serverName, InsecureSkipVerify: o.Insecure, MinVersion: tls.VersionTLS12}
	if o.CAFile != "" {
		pem, err := os.ReadFile(o.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in %s", o.CAFile)
		}
		cfg.RootCAs = pool
	}
	if o.CertFile != "" || o.KeyFile != "" {
		if o.CertFile == "" || o.KeyFile == "" {
			return nil, errors.New("both a client certificate and key are needed")
		}
		cert, err := tls.LoadX509KeyPair(o.CertFile, o.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// DefaultClientID returns a client ID unique to this process.
func DefaultClientID() string {
	host, _ := os.Hostname()
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	if host == "" {
		host = "host"
	}
	return fmt.Sprintf("mqmon-%s-%d", host, os.Getpid())
}

// ParseBroker turns "host", "host:port" or a URL into a broker URL. The port
// argument (0 for unset) overrides the one in s; tls forces a secure scheme.
func ParseBroker(s string, port int, useTLS bool) (*url.URL, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("no broker host given")
	}
	if !strings.Contains(s, "://") {
		s = "mqtt://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("invalid broker %q: %w", s, err)
	}
	switch u.Scheme {
	case "tcp", "mqtt":
		u.Scheme = "mqtt"
	case "ssl", "tls", "mqtts", "tcps":
		u.Scheme = "mqtts"
	case "ws", "wss":
	default:
		return nil, fmt.Errorf("unsupported scheme %q (use mqtt, mqtts, ws or wss)", u.Scheme)
	}
	if useTLS {
		switch u.Scheme {
		case "mqtt":
			u.Scheme = "mqtts"
		case "ws":
			u.Scheme = "wss"
		}
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("invalid broker %q: no host", s)
	}
	if port > 0 {
		u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	} else if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(defaultPort(u.Scheme)))
	}
	if (u.Scheme == "ws" || u.Scheme == "wss") && u.Path == "" {
		u.Path = "/mqtt"
	}
	return u, nil
}

func defaultPort(scheme string) int {
	switch scheme {
	case "mqtts":
		return 8883
	case "ws":
		return 80
	case "wss":
		return 443
	default:
		return 1883
	}
}

// Match reports whether topic matches the MQTT topic filter, which may use
// the + (single level) and # (multi level) wildcards.
func Match(filter, topic string) bool {
	if filter == "#" {
		// # does not match topics starting with $ (e.g. $SYS).
		return !strings.HasPrefix(topic, "$")
	}
	f := strings.Split(filter, "/")
	t := strings.Split(topic, "/")
	if len(t) > 0 && strings.HasPrefix(t[0], "$") && (f[0] == "+" || f[0] == "#") {
		return false
	}
	for i, part := range f {
		if part == "#" {
			return true
		}
		if i >= len(t) {
			return false
		}
		if part != "+" && part != t[i] {
			return false
		}
	}
	return len(f) == len(t)
}

// ValidFilter reports whether filter is a valid subscription topic filter.
func ValidFilter(filter string) error {
	if filter == "" {
		return errors.New("topic filter is empty")
	}
	parts := strings.Split(filter, "/")
	for i, p := range parts {
		if strings.Contains(p, "#") && (p != "#" || i != len(parts)-1) {
			return errors.New("# must be the whole last level")
		}
		if strings.Contains(p, "+") && p != "+" {
			return errors.New("+ must be a whole level")
		}
	}
	return nil
}

// ValidTopic reports whether topic can be published to.
func ValidTopic(topic string) error {
	if topic == "" {
		return errors.New("topic is empty")
	}
	if strings.ContainsAny(topic, "+#") {
		return errors.New("wildcards cannot be used when publishing")
	}
	return nil
}
