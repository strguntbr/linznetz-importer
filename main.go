package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"linznetz-import/internal/config"
	"linznetz-import/internal/exporter"
	"linznetz-import/internal/source"
	"linznetz-import/internal/state"
	"linznetz-import/internal/util"
)

func main() {
	// 0. Parse command line flags
	outputMode := flag.String("output", "influx", "Output mode: influx (send via UDP), debug (print line protocol), graphv (vertical diagram), graph (horizontal diagram)")
	selectMode := flag.String("select", "unread", "Selection mode: unread, latest, user, missing, or file:<path>")
	debugBrowserPort := flag.Int("debugBrowserPort", 0, "Connect to existing browser on this port (remote debugging)")
	meterFlag := flag.String("meter", "", "Specify a single meter ID to process (e.g., AT0031...)")
	flag.Parse()

	if *selectMode == "user" && !config.IsInteractive {
		log.Fatal("Selection mode 'user' requires an interactive terminal.")
	}

	// 1. Setup Timezone
	tzName := util.GetEnv("TIMEZONE", "Europe/Vienna")
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		log.Fatalf("Error loading location %s: %v", tzName, err)
	}

	measurement := util.GetEnv("MEASUREMENT_NAME", "energy_flow")
	stateFilePath := util.GetEnv("STATE_FILE_PATH", "state.json")
	s := state.Load(stateFilePath)

	// 2. Setup Exporter
	var exp exporter.Exporter
	switch *outputMode {
	case "influx":
		influxHost := config.RequireEnv("INFLUX_HOST", false)
		influxPort := config.RequireEnv("INFLUX_PORT", false)
		udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%s", influxHost, influxPort))
		if err != nil {
			log.Fatalf("Error resolving UDP address: %v", err)
		}
		conn, err := net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			log.Fatalf("Error connecting to UDP: %v", err)
		}
		defer conn.Close()
		exp = &exporter.InfluxUDPExporter{
			Conn:        conn,
			Measurement: measurement,
			SelectMode:  *selectMode,
		}
	case "debug":
		exp = &exporter.DebugExporter{
			Measurement: measurement,
		}
	case "graphv":
		exp = &exporter.DiagramExporter{Horizontal: false}
	case "graph":
		exp = &exporter.DiagramExporter{Horizontal: true}
	default:
		log.Fatalf("Invalid output mode: %s", *outputMode)
	}

	// 3. Handle Sources
	if strings.HasPrefix(*selectMode, "file:") {
		filePath := strings.TrimPrefix(*selectMode, "file:")
		source.RunFileMode(filePath, exp, loc, s, stateFilePath)
		return
	}

	if *selectMode == "missing" {
		source.RunWebMode(exp, loc, s, stateFilePath, *debugBrowserPort, *meterFlag)
		return
	}

	source.RunMailMode(*selectMode, exp, loc, s, stateFilePath, *meterFlag)
}
