package app

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/paul-ridgway/mqtt-monitor/internal/mqttx"
)

// runPlain prints messages as "topic payload" lines, like mosquitto_sub -v,
// until interrupted.
func runPlain(opts mqttx.Options, subs []mqttx.Subscription, logger *messageLog, timestamps bool, done func(mqttx.Client)) error {
	out := bufio.NewWriter(os.Stdout)
	var mu sync.Mutex
	flush := time.NewTicker(100 * time.Millisecond)
	defer flush.Stop()
	go func() {
		for range flush.C {
			mu.Lock()
			out.Flush()
			mu.Unlock()
		}
	}()

	var lastErr string
	emit := func(ev any) {
		switch ev := ev.(type) {
		case mqttx.MessageEvent:
			m := ev.Message
			if logger != nil {
				logger.Write(m)
			}
			mu.Lock()
			if timestamps {
				out.WriteString(m.Time.Format("2006-01-02T15:04:05.000 "))
			}
			out.WriteString(m.Topic)
			out.WriteByte(' ')
			out.Write(m.Payload)
			out.WriteByte('\n')
			mu.Unlock()
		case mqttx.StateEvent:
			switch {
			case ev.State == mqttx.StateConnected:
				lastErr = ""
				fmt.Fprintf(os.Stderr, "connected to %s\n", opts.URL)
			case ev.Err != nil && ev.Err.Error() != lastErr:
				lastErr = ev.Err.Error()
				fmt.Fprintf(os.Stderr, "%s: %v\n", ev.State, ev.Err)
			}
		}
	}
	client, err := mqttx.Connect(opts, subs, emit)
	if err != nil {
		return err
	}
	done(nil)
	waitForSignal()
	client.Close()
	mu.Lock()
	out.Flush()
	mu.Unlock()
	done(client)
	return nil
}

// messageLog appends messages to a JSON lines file.
type messageLog struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

type logRecord struct {
	Time       time.Time `json:"time"`
	Topic      string    `json:"topic"`
	QoS        byte      `json:"qos"`
	Retained   bool      `json:"retained,omitempty"`
	Payload    *string   `json:"payload,omitempty"`
	PayloadB64 string    `json:"payload_b64,omitempty"`
}

func openLog(path string) (*messageLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &messageLog{f: f, enc: json.NewEncoder(f)}, nil
}

func (l *messageLog) Write(m *mqttx.Message) {
	r := logRecord{Time: m.Time, Topic: m.Topic, QoS: m.QoS, Retained: m.Retained}
	if utf8.Valid(m.Payload) {
		s := string(m.Payload)
		r.Payload = &s
	} else {
		r.PayloadB64 = base64.StdEncoding.EncodeToString(m.Payload)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.enc.Encode(r)
}

func (l *messageLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
