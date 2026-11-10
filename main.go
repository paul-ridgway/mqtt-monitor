// Command mqtt-monitor is a terminal client for watching and publishing to an
// MQTT broker.
package main

import (
	"os"

	"github.com/paul-ridgway/mqtt-monitor/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args))
}
