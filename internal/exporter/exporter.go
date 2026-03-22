package exporter

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"linznetz-import/internal/models"
	"linznetz-import/internal/state"
	"linznetz-import/internal/util"
)

type Exporter interface {
	Export(meterID string, points []models.DataPoint, direction string, s *models.State, stateFilePath string) error
}

// --- InfluxDB Exporter ---

type InfluxUDPExporter struct {
	Conn        *net.UDPConn
	Measurement string
	SelectMode  string
}

func (e *InfluxUDPExporter) Export(meterID string, points []models.DataPoint, direction string, s *models.State, stateFilePath string) error {
	shortID := util.ShortenMeterID(meterID)
	var lines []string
	ms := s.Meters[meterID]
	maxFinal := ms.LatestFinal
	maxIntermediate := ms.LatestIntermediate

	for _, p := range points {
		fields := fmt.Sprintf("total=%.4f,status=%q", p.Total, p.Status)
		if p.Rest != nil {
			fields += fmt.Sprintf(",rest=%.4f", *p.Rest)
		}
		if p.EEG != nil {
			fields += fmt.Sprintf(",eeg=%.4f", *p.EEG)
		}

		line := fmt.Sprintf("%s,meter_id=%s,dir=%s %s %d",
			e.Measurement, shortID, direction, fields, p.Time.UnixNano())
		lines = append(lines, line)

		if p.Status == "M" {
			if p.Time.After(maxFinal) {
				maxFinal = p.Time
			}
		}
		if p.Time.After(maxIntermediate) {
			maxIntermediate = p.Time
		}
	}

	if e.Conn != nil {
		for _, line := range lines {
			_, err := e.Conn.Write([]byte(line + "\n"))
			if err != nil {
				return err
			}
		}
		log.Printf("Sent %d points for meter %s (%s).", len(points), meterID, direction)
	} else {
		// This is for DebugExporter (which also uses this block if I'm not careful,
		// but I'll handle it separately below or consolidate)
	}

	if maxFinal.After(ms.LatestFinal) || maxIntermediate.After(ms.LatestIntermediate) {
		ms.LatestFinal = maxFinal
		ms.LatestIntermediate = maxIntermediate
		s.Meters[meterID] = ms
		state.Save(stateFilePath, s)
		log.Printf("Updated state for meter %s: Final=%v, Intermediate=%v",
			meterID, maxFinal.Format("2006-01-02 15:04"), maxIntermediate.Format("2006-01-02 15:04"))
	}

	return nil
}

// --- Debug Exporter ---

type DebugExporter struct {
	Measurement string
}

func (e *DebugExporter) Export(meterID string, points []models.DataPoint, direction string, s *models.State, stateFilePath string) error {
	shortID := util.ShortenMeterID(meterID)
	var lines []string
	ms := s.Meters[meterID]
	maxFinal := ms.LatestFinal
	maxIntermediate := ms.LatestIntermediate

	for _, p := range points {
		fields := fmt.Sprintf("total=%.4f,status=%q", p.Total, p.Status)
		if p.Rest != nil {
			fields += fmt.Sprintf(",rest=%.4f", *p.Rest)
		}
		if p.EEG != nil {
			fields += fmt.Sprintf(",eeg=%.4f", *p.EEG)
		}

		line := fmt.Sprintf("%s,meter_id=%s,dir=%s %s %d",
			e.Measurement, shortID, direction, fields, p.Time.UnixNano())
		lines = append(lines, line)

		if p.Status == "M" {
			if p.Time.After(maxFinal) {
				maxFinal = p.Time
			}
		}
		if p.Time.After(maxIntermediate) {
			maxIntermediate = p.Time
		}
	}

	for _, line := range lines {
		fmt.Println(line)
	}

	if maxFinal.After(ms.LatestFinal) || maxIntermediate.After(ms.LatestIntermediate) {
		fmt.Printf("State would be updated for meter %s to: Final=%v, Intermediate=%v\n",
			meterID, maxFinal.Format("2006-01-02 15:04"), maxIntermediate.Format("2006-01-02 15:04"))
	}

	return nil
}

// --- Diagram Exporter ---

type DiagramExporter struct {
	Horizontal bool
}

func (e *DiagramExporter) Export(meterID string, points []models.DataPoint, direction string, s *models.State, stateFilePath string) error {
	if e.Horizontal {
		drawHorizontalDiagram(points, direction)
	} else {
		drawDiagram(points, direction)
	}
	return nil
}

func drawDiagram(points []models.DataPoint, direction string) {
	maxVal := 0.0
	for _, p := range points {
		if p.Total > maxVal {
			maxVal = p.Total
		}
	}

	title := "Import"
	if direction == "export" {
		title = "Export"
	}

	fmt.Printf("\nDaily Energy %s Diagram (Stacked: EEG=#, Rest==)\n", title)
	fmt.Println("Time          | [EEG + Rest] Visualization")
	fmt.Println("--------------|--------------------------------------------------")

	terminalWidth := 50
	for _, p := range points {
		eegChars, restChars := 0, 0
		if maxVal > 0 {
			if p.EEG != nil {
				eegChars = int((*p.EEG / maxVal) * float64(terminalWidth))
			}
			if p.Rest != nil {
				restChars = int((*p.Rest / maxVal) * float64(terminalWidth))
			}
		}
		bar := strings.Repeat("#", eegChars) + strings.Repeat("=", restChars)
		fmt.Printf("%s | %-50s %.3f kWh (Status: %s)\n", p.Time.Format("15:04"), bar, p.Total, p.Status)
	}
}

func useColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	fileInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

func drawHorizontalDiagram(points []models.DataPoint, direction string) {
	if len(points) == 0 {
		return
	}

	maxTotal := 0.0
	for _, p := range points {
		if p.Total > maxTotal {
			maxTotal = p.Total
		}
	}

	if maxTotal == 0 {
		fmt.Println("No non-zero data points to display.")
		return
	}

	title := "Import"
	if direction == "export" {
		title = "Export"
	}

	niceSteps := []float64{
		0.001, 0.002, 0.005,
		0.01, 0.02, 0.05,
		0.1, 0.2, 0.5,
		1.0, 2.0, 5.0,
		10.0, 20.0, 50.0,
	}

	stepSize := 1.0
	maxLines := 30

	for _, s := range niceSteps {
		lines := int(maxTotal / s)
		if lines >= 20 && lines <= 40 {
			stepSize = s
			maxLines = lines + 1
			break
		}
	}
	if int(maxTotal/stepSize) > 40 {
		stepSize = maxTotal / 30
		maxLines = 30
	}

	colorEnabled := useColor()
	var eegChar, restChar string
	if colorEnabled {
		eegChar = "\033[32m█\033[0m" // Green
		restChar = "\033[31m█\033[0m" // Red
	} else {
		eegChar = "█" // Full block
		restChar = "▒" // Medium shade
	}

	fmt.Printf("\nDaily Energy %s (Max: %.3f kWh, Step: %.3f kWh)\n", title, maxTotal, stepSize)
	fmt.Printf("EEG=%s, Rest=%s (Stacked: EEG at bottom)\n\n", eegChar, restChar)

	for row := maxLines; row >= 1; row-- {
		rowVal := float64(row) * stepSize
		if row%5 == 0 || row == maxLines {
			fmt.Printf("%6.3f | ", rowVal)
		} else {
			fmt.Print("       | ")
		}

		for _, p := range points {
			totalInSteps := int(p.Total / stepSize)
			eegInSteps := 0
			if p.EEG != nil {
				eegInSteps = int(*p.EEG / stepSize)
			}

			if row > totalInSteps {
				fmt.Print(" ")
			} else if row > eegInSteps {
				fmt.Print(restChar)
			} else {
				fmt.Print(eegChar)
			}
		}
		fmt.Println()
	}

	fmt.Print("       +-" + strings.Repeat("-", len(points)) + "\n")
	fmt.Print("         ")

	for i := 0; i < len(points); i++ {
		if i%(96/4) == 0 {
			hour := (i / 4) % 24
			fmt.Printf("%02d", hour)
			i++
		} else {
			fmt.Print(" ")
		}
	}
	fmt.Println("\n(Time Axis: 00h to 24h)")
}
