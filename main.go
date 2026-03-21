package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
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

	"github.com/chromedp/chromedp"
	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/runtime"
	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
	"golang.org/x/term"
)

var isInteractive bool

func init() {
	isInteractive = term.IsTerminal(int(os.Stdin.Fd()))
}

func requireEnv(key string, sensitive bool) string {
	val := os.Getenv(key)
	if val != "" {
		return val
	}

	if !isInteractive {
		log.Fatalf("Missing required environment variable: %s", key)
	}

	fmt.Printf("Please enter %s: ", key)
	if sensitive {
		byteVal, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			log.Fatalf("Error reading %s: %v", key, err)
		}
		fmt.Println() // Newline after password
		val = string(byteVal)
	} else {
		fmt.Scanln(&val)
	}

	if val == "" {
		log.Fatalf("Required environment variable %s cannot be empty.", key)
	}
	// Set it so subsequent calls find it
	os.Setenv(key, val)
	return val
}

type DataPoint struct {
	Time   time.Time
	Total  float64
	Rest   *float64
	EEG    *float64
	Status string
}

type State struct {
	LatestDates map[string]time.Time `json:"latest_dates"`
}

func main() {
	// 0. Parse command line flags
	outputMode := flag.String("output", "influx", "Output mode: influx (send via UDP), debug (print line protocol), graphv (vertical diagram), graph (horizontal diagram)")
	selectMode := flag.String("select", "unread", "Selection mode: unread, latest, user, missing, or file:<path>")
	debugBrowserPort := flag.Int("debugBrowserPort", 0, "Connect to existing browser on this port (remote debugging)")
	meterFlag := flag.String("meter", "", "Specify a single meter ID to process (e.g., AT0031...)")
	flag.Parse()

	if *selectMode == "user" && !isInteractive {
		log.Fatal("Selection mode 'user' requires an interactive terminal.")
	}

	// 1. Setup Timezone
	tzName := getEnv("TIMEZONE", "Europe/Vienna")
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		log.Fatalf("Error loading location %s: %v", tzName, err)
	}

	measurement := getEnv("MEASUREMENT_NAME", "energy_flow")

	stateFilePath := getEnv("STATE_FILE_PATH", "state.json")
	state := loadState(stateFilePath)

	// 2. Setup UDP Connection (only if output is influx)
	var conn *net.UDPConn
	if *outputMode == "influx" {
		influxHost := requireEnv("INFLUX_HOST", false)
		influxPort := requireEnv("INFLUX_PORT", false)
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
		runFileMode(filePath, *outputMode, measurement, loc, conn, state, stateFilePath)
		return
	}

	// 4. Handle Web Interface Mode
	if *selectMode == "missing" {
		runWebMode(*outputMode, measurement, loc, conn, state, stateFilePath, *debugBrowserPort, *meterFlag)
		return
	}

	// 5. Handle Gmail/IMAP Modes
	runMailMode(*selectMode, *outputMode, measurement, loc, conn, state, stateFilePath, *meterFlag)
}

func loadState(path string) *State {
	s := &State{LatestDates: make(map[string]time.Time)}
	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()
	json.NewDecoder(f).Decode(s)
	return s
}

func saveState(path string, s *State) {
	f, err := os.Create(path)
	if err != nil {
		log.Printf("Error saving state: %v", err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.Encode(s)
}
func runWebMode(outputMode, measurement string, loc *time.Location, conn *net.UDPConn, state *State, stateFilePath string, debugPort int, targetMeter string) {
	var allocCtx context.Context
	var cancel context.CancelFunc

	if debugPort > 0 {
		remoteURL := fmt.Sprintf("http://localhost:%d", debugPort)
		log.Printf("Connecting to existing browser on %s...", remoteURL)
		allocCtx, cancel = chromedp.NewRemoteAllocator(context.Background(), remoteURL)
	} else {
		log.Println("Starting standalone headless browser...")
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.NoSandbox,
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("disable-extensions", true),
		)
		allocCtx, cancel = chromedp.NewExecAllocator(context.Background(), opts...)
	}
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// 1. Diagnostics
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if ev, ok := ev.(*runtime.EventConsoleAPICalled); ok {
			for _, arg := range ev.Args {
				log.Printf("BROWSER CONSOLE: %s", arg.Value)
			}
		}
	})

	// 2. Login
	if debugPort == 0 {
		username := requireEnv("LINZNETZ_USER", false)
		password := requireEnv("LINZNETZ_PASSWORD", true)

		log.Println("Performing login...")
		err := chromedp.Run(ctx,
			chromedp.Navigate("https://www.linznetz.at/portal/de/home/online_services/serviceportal/mein_serviceportal"),
			chromedp.WaitVisible("#username", chromedp.ByID),
			chromedp.SendKeys("#username", username, chromedp.ByID),
			chromedp.SendKeys("#password", password, chromedp.ByID),
			chromedp.Click("button[type=\"submit\"]", chromedp.ByQuery),
			chromedp.WaitVisible("//a[contains(text(), \"Verbrauchsdateninformation\")]", chromedp.BySearch),
			chromedp.Click("//a[contains(text(), \"Verbrauchsdateninformation\")]", chromedp.BySearch),
			chromedp.WaitVisible("//a[contains(text(), \"Meine Verbräuche anzeigen\")]", chromedp.BySearch),
			chromedp.Click("//a[contains(text(), \"Meine Verbräuche anzeigen\")]", chromedp.BySearch),
		)
		if err != nil {
			log.Fatalf("Error during login/navigation: %v", err)
		}
	} else {
		targetURL := "https://services.linznetz.at/verbrauchsdateninformation/consumption.jsf?nav=%2Fde%2Flinz_netz_website%2Fonline_services%2Fserviceportal%2Fmeine_verbraeuche%2Fverbrauchsdateninformation%2Fverbrauchsdateninformation.xhtml"
		err := chromedp.Run(ctx, chromedp.Navigate(targetURL))
		if err != nil {
			log.Fatalf("Error navigating: %v", err)
		}
	}

	err := chromedp.Run(ctx,
		chromedp.WaitVisible("#myForm1", chromedp.ByQuery),
	)
	if err != nil {
		log.Fatalf("Error waiting for form: %v", err)
	}

	// 3. Dynamic Meter Discovery
	type MeterInfo struct {
		RadioID string `json:"radio_id"`
		MeterID string `json:"meter_id"`
		Label   string `json:"label"`
	}
	var meters []MeterInfo
	
	log.Println("Discovering meters...")
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`
			(function() {
				// Iterate over all divs with class "row"
				const rows = Array.from(document.querySelectorAll('div.row'));
				const discoveredMeters = [];
				
				rows.forEach(row => {
					// Check if this row has exactly one radio input for plantSelection
					const radios = Array.from(row.querySelectorAll('input[name="plantSelection"]'));
					if (radios.length === 1) {
						const r = radios[0];
						const text = row.innerText + " " + row.textContent;
						const match = text.match(/AT\d+/);
						
						const labelEl = row.querySelector('label[for="' + r.id + '"]') || row.querySelector('label');
						const label = labelEl ? labelEl.innerText.trim() : "Unknown";
						
						discoveredMeters.push({
							radio_id: r.id,
							meter_id: match ? match[0] : "",
							label: label
						});
					}
				});
				return discoveredMeters;
			})()
		`, &meters),
	)
	if err != nil {
		log.Fatalf("Error discovering meters: %v", err)
	}

	log.Printf("Discovered %d meters.", len(meters))
	
	// 4. Filter Meters
	var metersToProcess []MeterInfo
	if targetMeter != "" {
		found := false
		for _, m := range meters {
			if m.MeterID == targetMeter {
				metersToProcess = append(metersToProcess, m)
				found = true
				break
			}
		}
		if !found {
			log.Fatalf("Error: Specified meter %s not found in portal.", targetMeter)
		}
	} else {
		metersToProcess = meters
	}

	tmpDir, err := os.MkdirTemp("", "linznetz-csv")
	if err != nil {
		log.Fatalf("Error creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	yesterday := time.Now().In(loc).AddDate(0, 0, -1)
	todayStr := time.Now().In(loc).Format("02.01.2006")

	for _, m := range metersToProcess {
		if m.MeterID == "" {
			log.Printf("Skipping meter with no ID (Radio: %s, Label: %s)", m.RadioID, m.Label)
			continue
		}
		
		log.Printf("Processing meter: %s (%s)", m.MeterID, m.Label)

		var fromDate time.Time
		
		// 1. Check State
		if lastDate, ok := state.LatestDates[m.MeterID]; ok && !lastDate.IsZero() {
			fromDate = lastDate
		} else {
			// 2. Check START_<MeterID>
			envKey := "START_" + m.MeterID
			if t := parseDateEnv(envKey, loc); t != nil {
				fromDate = *t
			} else {
				// 3. Check START_DATE
				if t := parseDateEnv("START_DATE", loc); t != nil {
					fromDate = *t
				} else {
					// 4. Fallback to yesterday
					fromDate = yesterday
				}
			}
			// Don't persist yet, only after success
		}
		
		if fromDate.After(yesterday) {
			log.Printf("Meter %s already up to date (%s). Skipping.", m.MeterID, state.LatestDates[m.MeterID].Format("02.01.2006"))
			continue
		}
		
		fromStr := fromDate.Format("02.01.2006")
		log.Printf("Selecting meter %s (%s) and requesting data from %s to %s...", m.MeterID, m.Label, fromStr, todayStr)

		var radioVal string
		var nodes []*cdp.Node
		err := chromedp.Run(ctx,
			// 1. Get value and select
			chromedp.Value("#"+m.RadioID, &radioVal, chromedp.ByID),
			chromedp.ActionFunc(func(ctx context.Context) error {
				return chromedp.Evaluate(fmt.Sprintf("selectPlant('%s')", radioVal), nil).Do(ctx)
			}),
			chromedp.Sleep(2*time.Second),

			// 2. Set 'Viertelstundenwerte'
			chromedp.Click("label[for=\"myForm1:j_idt1435:grid_eval:selectedClass:1\"]", chromedp.ByQuery),
			chromedp.Sleep(1*time.Second),

			// 3. Set Dates
			chromedp.SetValue("#myForm1\\:calendarFromRegion", fromStr, chromedp.ByQuery),
			chromedp.Sleep(1*time.Second),
			chromedp.SetValue("#myForm1\\:calendarToRegion", todayStr, chromedp.ByQuery),
			chromedp.Sleep(1*time.Second),
			
			// 4. Click Anzeigen
			chromedp.Click("#myForm1\\:btnIdA1", chromedp.ByQuery),
			chromedp.Sleep(3*time.Second),
			
			// Check export button
			chromedp.Nodes("#myForm1\\:exportAreaID\\:s100\\:button1", &nodes, chromedp.AtLeast(0)),
		)

		if err != nil {
			log.Printf("Error processing meter %s: %v", m.MeterID, err)
			continue
		}

		if len(nodes) == 0 {
			log.Printf("No export button found for meter %s. Likely no data. Skipping.", m.MeterID)
			continue
		}

		log.Printf("Export button found for meter %s. Extracting command...", m.MeterID)
		
		var onclick string
		err = chromedp.Run(ctx,
			chromedp.AttributeValue("#myForm1\\:exportAreaID\\:s100\\:button1", "onclick", &onclick, nil),
		)
		if err != nil || onclick == "" {
			log.Printf("Could not find onclick handler for meter %s: %v", m.MeterID, err)
			continue
		}

		jsCommand := strings.Replace(onclick, "'_blank'", "'_self'", 1)
		jsCommand = strings.Replace(jsCommand, "return false", "", 1)
		
		var completed = make(chan bool, 1)
		chromedp.ListenTarget(ctx, func(ev interface{}) {
			switch ev := ev.(type) {
			case *browser.EventDownloadProgress:
				if ev.State == browser.DownloadProgressStateCompleted {
					completed <- true
				} else if ev.State == browser.DownloadProgressStateCanceled {
					completed <- false
				}
			}
		})

		err = chromedp.Run(ctx,
			browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).WithDownloadPath(tmpDir).WithEventsEnabled(true),
			chromedp.Evaluate(jsCommand, nil),
		)
		if err != nil {
			log.Printf("Error triggering download for meter %s: %v", m.MeterID, err)
			continue
		}

		log.Println("Waiting for download completion event...")
		downloadDone := false
		select {
		case success := <-completed:
			if success {
				log.Println("Download event received: Completed.")
				downloadDone = true
			} else {
				log.Printf("Download event received: Canceled.")
			}
		case <-time.After(20 * time.Second):
			log.Printf("Timeout waiting for download event. Checking directory manually...")
		}

		if !downloadDone {
			for i := 0; i < 5; i++ {
				files, _ := os.ReadDir(tmpDir)
				if len(files) > 0 {
					log.Printf("Found %d file(s) in temp dir via polling.", len(files))
					downloadDone = true
					break
				}
				time.Sleep(1 * time.Second)
			}
		}

		if !downloadDone {
			log.Printf("Failed to capture download for meter %s.", m.MeterID)
			continue
		}

		files, _ := os.ReadDir(tmpDir)
		for _, f := range files {
			path := tmpDir + "/" + f.Name()
			log.Printf("Processing downloaded file: %s (%d bytes)", path, func() int64 { s, _ := os.Stat(path); return s.Size() }())
			
			file, err := os.Open(path)
			if err == nil {
				points, direction := parseCSVToPoints(file, loc)
				file.Close()
				if len(points) > 0 {
					handleOutput(points, m.MeterID, direction, outputMode, measurement, conn, nil, 0, "missing", state, stateFilePath)
				}
			}
			os.Remove(path)
		}
	}
}
func runFileMode(filePath, outputMode, measurement string, loc *time.Location, conn *net.UDPConn, state *State, stateFilePath string) {
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

	handleOutput(points, meterID, direction, outputMode, measurement, conn, nil, 0, "", state, stateFilePath)
}

func runMailMode(selectMode, outputMode, measurement string, loc *time.Location, conn *net.UDPConn, state *State, stateFilePath string, targetMeter string) {
	gmailUser := requireEnv("GMAIL_USER", false)
	gmailPass := requireEnv("GMAIL_PASSWORD", true)
	gmailServer := getEnv("GMAIL_IMAP_SERVER", "imap.gmail.com:993")

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
		processEmailByID(c, id, outputMode, selectMode, measurement, loc, conn, state, stateFilePath, targetMeter)
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

func processEmailByID(c *client.Client, id uint32, outputMode, selectMode, measurement string, loc *time.Location, conn *net.UDPConn, state *State, stateFilePath string, targetMeter string) {
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
				if targetMeter != "" && meterID != targetMeter {
					log.Printf("Skipping email attachment for meter %s (requested: %s)", meterID, targetMeter)
					continue
				}
				if meterID != "" {
					points, direction := parseCSVToPoints(p.Body, loc)
					if len(points) > 0 {
						handleOutput(points, meterID, direction, outputMode, measurement, conn, c, id, selectMode, state, stateFilePath)
					}
				}
			}
		}
	}
}

func handleOutput(points []DataPoint, meterID, direction, outputMode, measurement string, conn *net.UDPConn, c *client.Client, emailID uint32, selectMode string, state *State, stateFilePath string) {
	shortID := shortenMeterID(meterID)
	
	// 1. Preparation Phase: Generate lines and calculate maxMTime
	var lines []string
	maxMTime := state.LatestDates[meterID]
	
	for _, p := range points {
		fields := fmt.Sprintf("total=%.4f,status=%q", p.Total, p.Status)
		if p.Rest != nil {
			fields += fmt.Sprintf(",rest=%.4f", *p.Rest)
		}
		if p.EEG != nil {
			fields += fmt.Sprintf(",eeg=%.4f", *p.EEG)
		}

		line := fmt.Sprintf("%s,meter_id=%s,dir=%s %s %d",
			measurement, shortID, direction, fields, p.Time.UnixNano())
		lines = append(lines, line)

		if p.Status == "M" {
			if p.Time.After(maxMTime) {
				maxMTime = p.Time
			}
		}
	}

	switch outputMode {
	case "influx":
		if conn != nil {
			for _, line := range lines {
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

		if maxMTime.After(state.LatestDates[meterID]) {
			state.LatestDates[meterID] = maxMTime
			saveState(stateFilePath, state)
			log.Printf("Updated state for meter %s: %v", meterID, maxMTime.Format("2006-01-02 15:04"))
		}
	case "debug":
		for _, line := range lines {
			fmt.Println(line)
		}
		
		if maxMTime.After(state.LatestDates[meterID]) {
			fmt.Printf("State would be updated for meter %s to: %v\n", meterID, maxMTime.Format("2006-01-02 15:04"))
		}
	case "graphv":
		drawDiagram(points, direction)
	case "graph":
		drawHorizontalDiagram(points, direction)
	}
}

func parseDateEnv(key string, loc *time.Location) *time.Time {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	// Try multiple formats
	formats := []string{"2006-01-02", "02.01.2006"}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, val, loc); err == nil {
			return &t
		}
	}
	log.Printf("Warning: Invalid date format in %s: %s", key, val)
	return nil
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func extractMeterID(filename string) string {
	re := regexp.MustCompile(`AT\d+`)
	match := re.FindString(filename)
	return match
}

func shortenMeterID(fullID string) string {
	re := regexp.MustCompile(`AT(\d+)`)
	match := re.FindStringSubmatch(fullID)
	if len(match) > 1 {
		rePrefix := regexp.MustCompile(`^00310+990*`)
		id := rePrefix.ReplaceAllString(match[1], "")
		if id == "" {
			return "0"
		}
		return id
	}
	return fullID
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


