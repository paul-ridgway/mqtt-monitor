package mqttx

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

type v5Client struct {
	cm     *autopaho.ConnectionManager
	cancel context.CancelFunc
	subs   *subSet
	emit   func(any)

	mu sync.Mutex
	id string
	up bool
}

func connectV5(opts Options, tlsCfg *tls.Config, subs *subSet, emit func(any)) (Client, error) {
	v := &v5Client{subs: subs, emit: emit, id: opts.ClientID}

	broker := *opts.URL
	broker.User = nil
	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{&broker},
		TlsCfg:                        tlsCfg,
		KeepAlive:                     uint16(opts.KeepAlive / time.Second),
		CleanStartOnInitialConnection: true,
		ReconnectBackoff:              autopaho.NewExponentialBackoff(time.Second, 30*time.Second, 2*time.Second, 2),
		ConnectTimeout:                10 * time.Second,
		ConnectUsername:               opts.Username,
		ConnectPassword:               []byte(opts.Password),
		OnConnectionUp: func(cm *autopaho.ConnectionManager, ack *paho.Connack) {
			v.mu.Lock()
			v.up = true
			if ack.Properties != nil && ack.Properties.AssignedClientID != "" {
				v.id = ack.Properties.AssignedClientID
			}
			v.mu.Unlock()
			v.emit(StateEvent{State: StateConnected})
			for _, s := range v.subs.list() {
				go v.subscribeNow(s)
			}
		},
		OnConnectionDown: func() bool {
			v.setUp(false)
			v.emit(StateEvent{State: StateDisconnected})
			return true
		},
		OnConnectError: func(err error) {
			var ce *autopaho.ConnackError
			if errors.As(err, &ce) {
				err = connackError(ce)
			}
			v.emit(StateEvent{State: StateDisconnected, Err: err})
		},
		ClientConfig: paho.ClientConfig{
			ClientID: opts.ClientID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) {
					v.emit(MessageEvent{Message: fromPaho(pr.Packet)})
					return true, nil
				},
			},
			OnServerDisconnect: func(d *paho.Disconnect) {
				var err error
				if d.Properties != nil && d.Properties.ReasonString != "" {
					err = fmt.Errorf("server disconnected: %s", d.Properties.ReasonString)
				} else {
					err = fmt.Errorf("server disconnected (reason code %d)", d.ReasonCode)
				}
				v.emit(StateEvent{State: StateDisconnected, Err: err})
			},
			OnClientError: func(err error) {
				v.emit(StateEvent{State: StateDisconnected, Err: err})
			},
		},
	}
	if opts.Username == "" {
		cfg.ConnectPassword = nil
	}
	if opts.KeepAlive > 0 && cfg.KeepAlive == 0 {
		cfg.KeepAlive = 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	v.emit(StateEvent{State: StateConnecting})
	cm, err := autopaho.NewConnection(ctx, cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	v.cm = cm
	return v, nil
}

var connackReasons = map[byte]string{
	0x80: "unspecified error",
	0x81: "malformed packet",
	0x82: "protocol error",
	0x84: "unsupported protocol version",
	0x85: "client identifier not valid",
	0x86: "bad user name or password",
	0x87: "not authorized",
	0x88: "server unavailable",
	0x89: "server busy",
	0x8A: "banned",
	0x8C: "bad authentication method",
	0x95: "packet too large",
	0x97: "quota exceeded",
	0x9C: "use another server",
	0x9D: "server moved",
	0x9F: "connection rate exceeded",
}

func connackError(ce *autopaho.ConnackError) error {
	reason := ce.Reason
	if reason == "" {
		reason = connackReasons[ce.ReasonCode]
	}
	if reason == "" {
		reason = fmt.Sprintf("reason code 0x%02x", ce.ReasonCode)
	}
	return fmt.Errorf("connection refused: %s", reason)
}

func fromPaho(p *paho.Publish) *Message {
	m := &Message{
		Topic:    p.Topic,
		Payload:  append([]byte(nil), p.Payload...),
		QoS:      p.QoS,
		Retained: p.Retain,
		Time:     time.Now(),
	}
	if p.Properties != nil {
		m.ContentType = p.Properties.ContentType
		m.ResponseTopic = p.Properties.ResponseTopic
		m.CorrelationData = p.Properties.CorrelationData
		for _, u := range p.Properties.User {
			m.UserProperties = append(m.UserProperties, [2]string{u.Key, u.Value})
		}
	}
	return m
}

func (v *v5Client) setUp(up bool) {
	v.mu.Lock()
	v.up = up
	v.mu.Unlock()
}

func (v *v5Client) isUp() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.up
}

func (v *v5Client) subscribeNow(s Subscription) error {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	ack, err := v.cm.Subscribe(ctx, &paho.Subscribe{
		Subscriptions: []paho.SubscribeOptions{{Topic: s.Topic, QoS: s.QoS}},
	})
	if err != nil {
		return describeAck(err, ack)
	}
	if ack != nil {
		for _, code := range ack.Reasons {
			if code >= 0x80 {
				return fmt.Errorf("broker refused subscription to %s (reason code 0x%02x)", s.Topic, code)
			}
		}
	}
	return nil
}

func describeAck(err error, ack *paho.Suback) error {
	if ack != nil && ack.Properties != nil && ack.Properties.ReasonString != "" {
		return fmt.Errorf("%w: %s", err, ack.Properties.ReasonString)
	}
	return err
}

func (v *v5Client) Subscribe(topic string, qos byte) error {
	if err := ValidFilter(topic); err != nil {
		return err
	}
	s := Subscription{Topic: topic, QoS: qos}
	if v.isUp() {
		if err := v.subscribeNow(s); err != nil {
			return err
		}
	}
	v.subs.add(s)
	return nil
}

func (v *v5Client) Unsubscribe(topic string) error {
	if !v.subs.remove(topic) {
		return fmt.Errorf("not subscribed to %s", topic)
	}
	if !v.isUp() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	_, err := v.cm.Unsubscribe(ctx, &paho.Unsubscribe{Topics: []string{topic}})
	return err
}

func (v *v5Client) Publish(p PublishOptions) error {
	if err := ValidTopic(p.Topic); err != nil {
		return err
	}
	if !v.isUp() {
		return errors.New("not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	pub := &paho.Publish{Topic: p.Topic, Payload: p.Payload, QoS: p.QoS, Retain: p.Retain}
	if p.ContentType != "" || p.ResponseTopic != "" {
		pub.Properties = &paho.PublishProperties{ContentType: p.ContentType, ResponseTopic: p.ResponseTopic}
	}
	resp, err := v.cm.Publish(ctx, pub)
	if err != nil {
		return err
	}
	if resp != nil && resp.ReasonCode >= 0x80 {
		if resp.Properties != nil && resp.Properties.ReasonString != "" {
			return fmt.Errorf("broker rejected publish: %s", resp.Properties.ReasonString)
		}
		return fmt.Errorf("broker rejected publish (reason code 0x%02x)", resp.ReasonCode)
	}
	return nil
}

func (v *v5Client) Subscriptions() []Subscription { return v.subs.list() }

func (v *v5Client) ClientID() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.id
}

func (v *v5Client) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = v.cm.Disconnect(ctx)
	v.cancel()
	v.emit(StateEvent{State: StateClosed})
}
