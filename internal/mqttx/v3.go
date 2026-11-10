package mqttx

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const opTimeout = 10 * time.Second

type v3Client struct {
	c      mqtt.Client
	closed chan struct{}
	subs   *subSet
	id     string
	emit   func(any)
}

func connectV3(opts Options, tlsCfg *tls.Config, subs *subSet, emit func(any)) (Client, error) {
	v := &v3Client{subs: subs, id: opts.ClientID, emit: emit}

	broker := *opts.URL
	broker.User = nil
	switch broker.Scheme {
	case "mqtt":
		broker.Scheme = "tcp"
	case "mqtts":
		broker.Scheme = "ssl"
	}

	o := mqtt.NewClientOptions().
		AddBroker(broker.String()).
		SetClientID(opts.ClientID).
		SetKeepAlive(opts.KeepAlive).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(false).
		SetConnectTimeout(10 * time.Second).
		SetMaxReconnectInterval(30 * time.Second).
		SetOrderMatters(false).
		SetProtocolVersion(4)
	if opts.Username != "" {
		o.SetUsername(opts.Username)
		o.SetPassword(opts.Password)
	}
	if tlsCfg != nil {
		o.SetTLSConfig(tlsCfg)
	}
	o.SetDefaultPublishHandler(func(_ mqtt.Client, m mqtt.Message) {
		v.emit(MessageEvent{Message: &Message{
			Topic:    m.Topic(),
			Payload:  append([]byte(nil), m.Payload()...),
			QoS:      m.Qos(),
			Retained: m.Retained(),
			Time:     time.Now(),
		}})
	})
	o.SetOnConnectHandler(func(c mqtt.Client) {
		v.emit(StateEvent{State: StateConnected})
		// Clean sessions lose subscriptions, so restore them on every connect.
		for _, s := range v.subs.list() {
			go v.subscribeNow(s)
		}
	})
	o.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		v.emit(StateEvent{State: StateDisconnected, Err: err})
	})
	o.SetReconnectingHandler(func(mqtt.Client, *mqtt.ClientOptions) {
		v.emit(StateEvent{State: StateConnecting})
	})
	o.SetConnectionAttemptHandler(func(_ *url.URL, cfg *tls.Config) *tls.Config {
		v.emit(StateEvent{State: StateConnecting})
		return cfg
	})

	v.c = mqtt.NewClient(o)
	v.closed = make(chan struct{})
	go v.connectLoop()
	return v, nil
}

// connectLoop makes the initial connection, reporting each failure (such as
// bad credentials) and retrying with backoff. Once connected, Paho's
// auto-reconnect takes over.
func (v *v3Client) connectLoop() {
	delay := 2 * time.Second
	for {
		t := v.c.Connect()
		t.Wait()
		err := t.Error()
		if err == nil {
			return
		}
		v.emit(StateEvent{State: StateDisconnected, Err: err})
		select {
		case <-v.closed:
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 30*time.Second)
		v.emit(StateEvent{State: StateConnecting})
	}
}

func (v *v3Client) subscribeNow(s Subscription) error {
	t := v.c.Subscribe(s.Topic, s.QoS, nil)
	if !t.WaitTimeout(opTimeout) {
		return errors.New("timed out waiting for the broker")
	}
	if err := t.Error(); err != nil {
		return err
	}
	if st, ok := t.(*mqtt.SubscribeToken); ok {
		for _, code := range st.Result() {
			if code == 0x80 {
				return fmt.Errorf("broker refused subscription to %s", s.Topic)
			}
		}
	}
	return nil
}

func (v *v3Client) Subscribe(topic string, qos byte) error {
	if err := ValidFilter(topic); err != nil {
		return err
	}
	s := Subscription{Topic: topic, QoS: qos}
	if v.c.IsConnectionOpen() {
		if err := v.subscribeNow(s); err != nil {
			return err
		}
	}
	v.subs.add(s)
	return nil
}

func (v *v3Client) Unsubscribe(topic string) error {
	if !v.subs.remove(topic) {
		return fmt.Errorf("not subscribed to %s", topic)
	}
	if !v.c.IsConnectionOpen() {
		return nil
	}
	t := v.c.Unsubscribe(topic)
	if !t.WaitTimeout(opTimeout) {
		return errors.New("timed out waiting for the broker")
	}
	return t.Error()
}

func (v *v3Client) Publish(p PublishOptions) error {
	if err := ValidTopic(p.Topic); err != nil {
		return err
	}
	if !v.c.IsConnectionOpen() {
		return errors.New("not connected")
	}
	t := v.c.Publish(p.Topic, p.QoS, p.Retain, p.Payload)
	if !t.WaitTimeout(opTimeout) {
		return errors.New("timed out waiting for the broker")
	}
	return t.Error()
}

func (v *v3Client) Subscriptions() []Subscription { return v.subs.list() }

func (v *v3Client) ClientID() string { return v.id }

func (v *v3Client) Close() {
	close(v.closed)
	v.c.Disconnect(250)
	v.emit(StateEvent{State: StateClosed})
}
