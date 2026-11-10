// Command mqmon is a short alias for mqtt-monitor.
package main

import (
	"os"

	"github.com/paul-ridgway/mqtt-monitor/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args))
}
