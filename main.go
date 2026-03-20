package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
)

type DataPoint struct {
	Time   time.Time
	Total  float64
	Rest   *float64
	EEG    *float64
	Status string
}

func main() {
	// 0. Parse command line flags
	outputMode := flag.String("output", "influx", "Output mode: influx (send via UDP), debug (print line protocol), graphv (vertical diagram), graph (horizontal diagram)")
	selectMode := flag.String("select", "unread", "Selection mode: unread, latest, user, or file:<path>")
	flag.Parse()

	// 1. Setup Timezone
	tzName := getEnv("TIMEZONE", "Europe/Vienna")
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		log.Fatalf("Error loading location %s: %v", tzName, err)
	}

	measurement := getEnv("MEASUREMENT_NAME", "energy_flow")

	// 2. Setup UDP Connection (only if output is influx)
	var conn *net.UDPConn
	if *outputMode == "influx" {
		influxHost := os.Getenv("INFLUX_HOST")
		influxPort := os.Getenv("INFLUX_PORT")
		if influxHost == "" || influxPort == "" {
			log.Fatal("Missing required environment variables (INFLUX_HOST, INFLUX_PORT) for influx output")
		}
		udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%s", influxHost, influxPort))
		if err != nil {
			log.Fatalf("Error resolving UDP address: %v", err)
		}
		conn, err = net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			log.Fatalf("Error connecting to UDP: %v", err)
		}
		defer conn.Close()
	}

	// 3. Handle Local File Mode
	if strings.HasPrefix(*selectMode, "file:") {
		filePath := strings.TrimPrefix(*selectMode, "file:")
		runFileMode(filePath, *outputMode, measurement, loc, conn)
		return
	}

	// 4. Handle Gmail/IMAP Modes
	runMailMode(*selectMode, *outputMode, measurement, loc, conn)
}

func runFileMode(filePath, outputMode, measurement string, loc *time.Location, conn *net.UDPConn) {
	f, err := os.Open(filePath)
	if err != nil {
		log.Fatalf("Error opening file %s: %v", filePath, err)
	}
	defer f.Close()

	meterID := extractMeterID(filePath)
	if meterID == "" {
		meterID = "local-file"
	}

	points, direction := parseCSVToPoints(f, loc)
	if len(points) == 0 {
		log.Fatal("No data points found in file.")
	}

	handleOutput(points, meterID, direction, outputMode, measurement, conn, nil, 0, "")
}

func runMailMode(selectMode, outputMode, measurement string, loc *time.Location, conn *net.UDPConn) {
	gmailUser := os.Getenv("GMAIL_USER")
	gmailPass := os.Getenv("GMAIL_PASSWORD")
	gmailServer := getEnv("GMAIL_IMAP_SERVER", "imap.gmail.com:993")

	if gmailUser == "" || gmailPass == "" {
		log.Fatal("Missing required environment variables (GMAIL_USER, GMAIL_PASSWORD)")
	}

	c, err := client.DialTLS(gmailServer, nil)
	if err != nil {
		log.Fatalf("Error connecting to IMAP: %v", err)
	}
	defer c.Logout()

	if err := c.Login(gmailUser, gmailPass); err != nil {
		log.Fatalf("Error logging in: %v", err)
	}

	gmailLabel := getEnv("GMAIL_LABEL", "linz-netz-tagesbericht")
	_, err = c.Select(gmailLabel, false)
	if err != nil {
		log.Fatalf("Error selecting label %s: %v", gmailLabel, err)
	}

	subjectPrefix := "LINZ NETZ VDI - Tagesbericht Viertelstundenverbrauch"
	criteria := imap.NewSearchCriteria()
	criteria.Header.Set("Subject", subjectPrefix)
	
	if selectMode == "unread" {
		criteria.WithoutFlags = []string{imap.SeenFlag}
	}
	
	ids, err := c.Search(criteria)
	if err != nil {
		log.Fatalf("Error searching for emails: %v", err)
	}
	if len(ids) == 0 {
		log.Println("No matching emails found.")
		return
	}

	var selectedIDs []uint32
	switch selectMode {
	case "unread":
		selectedIDs = ids
	case "latest":
		selectedIDs = []uint32{ids[len(ids)-1]}
	case "user":
		selectedIDs = runUserSelection(c, ids)
	default:
		log.Fatalf("Invalid select mode: %s", selectMode)
	}

	for _, id := range selectedIDs {
		processEmailByID(c, id, outputMode, selectMode, measurement, loc, conn)
	}
}

func runUserSelection(c *client.Client, ids []uint32) []uint32 {
	const pageSize = 20
	page := 0

	for {
		start := page * pageSize
		if start >= len(ids) { start = len(ids) - pageSize }
		if start < 0 { start = 0 }
		
		end := start + pageSize
		if end > len(ids) { end = len(ids) }

		pageIDs := ids[start:end]
		
		seqset := new(imap.SeqSet)
		seqset.AddNum(pageIDs...)
		
		messages := make(chan *imap.Message, len(pageIDs))
		done := make(chan error, 1)
		go func() {
			done <- c.Fetch(seqset, []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags}, messages)
		}()

		var msgList []*imap.Message
		for msg := range messages {
			msgList = append(msgList, msg)
		}
		if err := <-done; err != nil {
			log.Fatal(err)
		}

		fmt.Printf("\n--- Page %d (Showing %d-%d of %d) ---\n", page+1, start+1, end, len(ids))
		for i, m := range msgList {
			isUnread := true
			for _, flag := range m.Flags {
				if flag == imap.SeenFlag {
					isUnread = false
					break
				}
			}
			unreadMark := "   "
			if isUnread { unreadMark = "(*)" }
			subject := shortenSubject(m.Envelope.Subject)
			fmt.Printf("[%d] %s %s (Date: %s)\n", start+i, unreadMark, subject, m.Envelope.Date.Format("2006-01-02 15:04"))
		}

		fmt.Printf("\nOptions: [0-%d] Select, [n] Next, [p] Prev: ", len(ids)-1)
		var input string
		fmt.Scanln(&input)

		if input == "n" {
			if end < len(ids) { page++ }
			continue
		} else if input == "p" {
			if page > 0 { page-- }
			continue
		}

		selection, err := strconv.Atoi(input)
		if err == nil && selection >= 0 && selection < len(ids) {
			return []uint32{ids[selection]}
		}
		fmt.Println("Invalid input.")
	}
}

func shortenSubject(subject string) string {
	s := strings.TrimPrefix(subject, "LINZ NETZ VDI - ")

	// Remove "Detail: <any space> 4232 Hagenberg im Mühlkreis, "
	reDetail := regexp.MustCompile(`Detail:\s+4232 Hagenberg im Mühlkreis,\s*`)
	s = reDetail.ReplaceAllString(s, "")

	// Remove the suffix " - AT...", ", AT...", or just " AT..."
	reSuffix := regexp.MustCompile(`\s*[, -]*\s*AT\d+$`)
	s = reSuffix.ReplaceAllString(s, "")

	return strings.TrimSpace(s)
}

func processEmailByID(c *client.Client, id uint32, outputMode, selectMode, measurement string, loc *time.Location, conn *net.UDPConn) {
	seqset := new(imap.SeqSet)
	seqset.AddNum(id)
	
	section := &imap.BodySectionName{Peek: true}
	msgChan := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqset, []imap.FetchItem{section.FetchItem()}, msgChan)
	}()
	
	msg := <-msgChan
	if msg == nil {
		if err := <-done; err != nil {
			log.Printf("Error fetching email %d: %v", id, err)
		}
		return
	}
	
	r := msg.GetBody(section)
	if r == nil { return }
	
	mr, err := mail.CreateReader(r)
	if err != nil {
		log.Printf("Error reading mail %d: %v", id, err)
		return
	}

	for {
		p, err := mr.NextPart()
		if err == io.EOF { break }
		if err != nil { break }

		switch h := p.Header.(type) {
		case *mail.AttachmentHeader:
			filename, _ := h.Filename()
			if strings.HasPrefix(filename, "AT") && strings.HasSuffix(filename, ".csv") {
				meterID := extractMeterID(filename)
				if meterID != "" {
					points, direction := parseCSVToPoints(p.Body, loc)
					if len(points) > 0 {
						handleOutput(points, meterID, direction, outputMode, measurement, conn, c, id, selectMode)
					}
				}
			}
		}
	}
}

func handleOutput(points []DataPoint, meterID, direction, outputMode, measurement string, conn *net.UDPConn, c *client.Client, emailID uint32, selectMode string) {
	switch outputMode {
	case "influx":
		if conn != nil {
			for _, p := range points {
				// Construct fields string
				fields := fmt.Sprintf("total=%.4f,status=%q", p.Total, p.Status)
				if p.Rest != nil {
					fields += fmt.Sprintf(",rest=%.4f", *p.Rest)
				}
				if p.EEG != nil {
					fields += fmt.Sprintf(",eeg=%.4f", *p.EEG)
				}

				line := fmt.Sprintf("%s,meter_id=%s,dir=%s %s %d",
					measurement, meterID, direction, fields, p.Time.UnixNano())
				conn.Write([]byte(line + "\n"))
			}
			log.Printf("Sent %d points for meter %s (%s).", len(points), meterID, direction)
		}
		if selectMode == "unread" && c != nil {
			seqset := new(imap.SeqSet)
			seqset.AddNum(emailID)
			c.Store(seqset, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.SeenFlag}, nil)
			log.Printf("Email %d marked as read.", emailID)
		}
	case "debug":
		for _, p := range points {
			fields := fmt.Sprintf("total=%.4f,status=%q", p.Total, p.Status)
			if p.Rest != nil {
				fields += fmt.Sprintf(",rest=%.4f", *p.Rest)
			}
			if p.EEG != nil {
				fields += fmt.Sprintf(",eeg=%.4f", *p.EEG)
			}
			fmt.Printf("%s,meter_id=%s,dir=%s %s %d\n",
				measurement, meterID, direction, fields, p.Time.UnixNano())
		}
	case "graphv":
		drawDiagram(points, direction)
	case "graph":
		drawHorizontalDiagram(points, direction)
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func extractMeterID(filename string) string {
	re := regexp.MustCompile(`AT(\d+)`)
	match := re.FindStringSubmatch(filename)
	if len(match) > 1 {
		id := match[1]
		id = strings.TrimPrefix(id, "0031000000099")
		id = strings.TrimLeft(id, "0")
		if id == "" {
			return "0"
		}
		return id
	}
	return ""
}

func parseCSVToPoints(r io.Reader, loc *time.Location) ([]DataPoint, string) {
	reader := csv.NewReader(r)
	reader.Comma = ';'
	reader.LazyQuotes = true
	
	header, err := reader.Read() // skip header
	if err != nil { return nil, "import" }

	direction := "import"
	if len(header) >= 5 && strings.Contains(header[4], "Restnetzueberschuss") {
		direction = "export"
	}

	var points []DataPoint
	for {
		record, err := reader.Read()
		if err == io.EOF { break }
		// Expecting at least 6 columns: ignore, Datum bis, total, ignore, rest, status
		if len(record) < 6 { continue }
		
		// Try both with and without seconds to be robust
		layoutWithSec := "02.01.2006 15:04:05"
		layoutWithoutSec := "02.01.2006 15:04"
		
		t, err := time.ParseInLocation(layoutWithSec, record[1], loc)
		if err != nil {
			t, err = time.ParseInLocation(layoutWithoutSec, record[1], loc)
			if err != nil {
				continue
			}
		}
		
		total := 0.0
		if record[2] != "" {
			total, _ = parseCommaFloat(record[2])
		}

		var restPtr, eegPtr *float64
		if record[4] != "" {
			rest, _ := parseCommaFloat(record[4])
			restPtr = &rest
			eeg := total - rest
			eegPtr = &eeg
		}

		status := record[5]

		points = append(points, DataPoint{
			Time:   t,
			Total:  total,
			Rest:   restPtr,
			EEG:    eegPtr,
			Status: status,
		})
	}
	return points, direction
}

func parseCommaFloat(s string) (float64, error) {
	s = strings.Replace(s, ",", ".", 1)
	return strconv.ParseFloat(s, 64)
}

func drawDiagram(points []DataPoint, direction string) {
	maxVal := 0.0
	for _, p := range points {
		if p.Total > maxVal { maxVal = p.Total }
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
	// Check if stdout is a terminal
	fileInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

func drawHorizontalDiagram(points []DataPoint, direction string) {
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

	// 1. Find a "nice" step size for the Y-axis
	// Goal: number of lines between 20 and 40
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
		// If it's more than 40, we'll keep the previous one if it was better, but this loop goes up.
		// If we already surpassed 40 lines with small steps, we will find a larger step.
	}
	// Final fallback if no step fits perfectly
	if int(maxTotal/stepSize) > 40 {
		stepSize = maxTotal / 30
		maxLines = 30
	}

	// 2. Setup characters
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

	// 3. Render from top to bottom
	for row := maxLines; row >= 1; row-- {
		rowVal := float64(row) * stepSize
		// Print label every 5 lines or at the top
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

	// 4. Footer line
	fmt.Print("       +-" + strings.Repeat("-", len(points)) + "\n")
	fmt.Print("         ")

	// Print time labels (assuming 15-min intervals)
	for i := 0; i < len(points); i++ {
		if i%(96/4) == 0 {
			hour := (i / 4) % 24
			fmt.Printf("%02d", hour)
			i++ // skip one char since we printed 2 chars
		} else {
			fmt.Print(" ")
		}
	}
	fmt.Println("\n(Time Axis: 00h to 24h)")
}


