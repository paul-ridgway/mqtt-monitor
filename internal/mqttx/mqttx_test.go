package mqttx

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		filter, topic string
		want          bool
	}{
		{"#", "a/b/c", true},
		{"#", "$SYS/broker", false},
		{"$SYS/#", "$SYS/broker/uptime", true},
		{"a/#", "a", true},
		{"a/#", "a/b/c", true},
		{"a/#", "b/c", false},
		{"a/+", "a/b", true},
		{"a/+", "a/b/c", false},
		{"a/+/c", "a/b/c", true},
		{"+/b", "$x/b", false},
		{"a/b", "a/b", true},
		{"a/b", "a/b/c", false},
		{"a/b/c", "a/b", false},
		{"+", "a", true},
		{"+", "a/b", false},
	}
	for _, c := range cases {
		if got := Match(c.filter, c.topic); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.filter, c.topic, got, c.want)
		}
	}
}

func TestValidFilter(t *testing.T) {
	for _, ok := range []string{"#", "a/#", "+", "a/+/b", "a/b"} {
		if err := ValidFilter(ok); err != nil {
			t.Errorf("ValidFilter(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a#", "a/#/b", "a+", "a/b+/c"} {
		if ValidFilter(bad) == nil {
			t.Errorf("ValidFilter(%q) succeeded", bad)
		}
	}
}

func TestParseBroker(t *testing.T) {
	cases := []struct {
		in    string
		port  int
		tls   bool
		want  string
		isErr bool
	}{
		{"broker", 0, false, "mqtt://broker:1883", false},
		{"broker:1884", 0, false, "mqtt://broker:1884", false},
		{"broker:1884", 2000, false, "mqtt://broker:2000", false},
		{"broker", 0, true, "mqtts://broker:8883", false},
		{"tcp://broker", 0, false, "mqtt://broker:1883", false},
		{"ssl://broker", 0, false, "mqtts://broker:8883", false},
		{"ws://broker", 0, false, "ws://broker:80/mqtt", false},
		{"wss://broker:9001/ws", 0, false, "wss://broker:9001/ws", false},
		{"ftp://broker", 0, false, "", true},
		{"", 0, false, "", true},
	}
	for _, c := range cases {
		u, err := ParseBroker(c.in, c.port, c.tls)
		if c.isErr {
			if err == nil {
				t.Errorf("ParseBroker(%q) succeeded", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseBroker(%q) = %v", c.in, err)
			continue
		}
		if u.String() != c.want {
			t.Errorf("ParseBroker(%q) = %s, want %s", c.in, u, c.want)
		}
	}
}
