package source

import (
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
	"regexp"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/runtime"
	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"

	"linznetz-import/internal/config"
	"linznetz-import/internal/exporter"
	"linznetz-import/internal/models"
	"linznetz-import/internal/parser"
	"linznetz-import/internal/util"
)

// --- Local File Source ---

func RunFileMode(filePath string, exp exporter.Exporter, loc *time.Location, s *models.State, stateFilePath string) {
	f, err := os.Open(filePath)
	if err != nil {
		log.Fatalf("Error opening file %s: %v", filePath, err)
	}
	defer f.Close()

	meterID := util.ExtractMeterID(filePath)
	if meterID == "" {
		meterID = "local-file"
	}

	points, direction := parser.ParseCSVToPoints(f, loc)
	if len(points) == 0 {
		log.Fatal("No data points found in file.")
	}

	exp.Export(meterID, points, direction, s, stateFilePath)
}

// --- Mail Source ---

func RunMailMode(selectMode string, exp exporter.Exporter, loc *time.Location, s *models.State, stateFilePath string, targetMeter string) {
	gmailUser := config.RequireEnv("GMAIL_USER", false)
	gmailPass := config.RequireEnv("GMAIL_PASSWORD", true)
	gmailServer := util.GetEnv("GMAIL_IMAP_SERVER", "imap.gmail.com:993")

	c, err := client.DialTLS(gmailServer, nil)
	if err != nil {
		log.Fatalf("Error connecting to IMAP: %v", err)
	}
	defer c.Logout()

	if err := c.Login(gmailUser, gmailPass); err != nil {
		log.Fatalf("Error logging in: %v", err)
	}

	gmailLabel := util.GetEnv("GMAIL_LABEL", "linz-netz-tagesbericht")
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
		processEmailByID(c, id, exp, loc, s, stateFilePath, targetMeter, selectMode)
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
	reDetail := regexp.MustCompile(`Detail:\s+4232 Hagenberg im Mühlkreis,\s*`)
	s = reDetail.ReplaceAllString(s, "")
	reSuffix := regexp.MustCompile(`\s*[, -]*\s*AT\d+$`)
	s = reSuffix.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func processEmailByID(c *client.Client, id uint32, exp exporter.Exporter, loc *time.Location, s *models.State, stateFilePath string, targetMeter string, selectMode string) {
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
				meterID := util.ExtractMeterID(filename)
				if targetMeter != "" && meterID != targetMeter {
					log.Printf("Skipping email attachment for meter %s (requested: %s)", meterID, targetMeter)
					continue
				}
				if meterID != "" {
					points, direction := parser.ParseCSVToPoints(p.Body, loc)
					if len(points) > 0 {
						exp.Export(meterID, points, direction, s, stateFilePath)
						
						// Mark as read only if influx output was successful
						if selectMode == "unread" {
							readSeqSet := new(imap.SeqSet)
							readSeqSet.AddNum(id)
							c.Store(readSeqSet, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.SeenFlag}, nil)
							log.Printf("Email %d marked as read.", id)
						}
					}
				}
			}
		}
	}
}

// --- Web Portal Source ---

func RunWebMode(exp exporter.Exporter, loc *time.Location, s *models.State, stateFilePath string, debugPort int, targetMeter string) {
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

	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if ev, ok := ev.(*runtime.EventConsoleAPICalled); ok {
			for _, arg := range ev.Args {
				log.Printf("BROWSER CONSOLE: %s", arg.Value)
			}
		}
	})

	if debugPort == 0 {
		username := config.RequireEnv("LINZNETZ_USER", false)
		password := config.RequireEnv("LINZNETZ_PASSWORD", true)

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

	var meters []models.MeterInfo
	log.Println("Discovering meters...")
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`
			(function() {
				const rows = Array.from(document.querySelectorAll('div.row'));
				const discoveredMeters = [];
				rows.forEach(row => {
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
	
	var metersToProcess []models.MeterInfo
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
		if lastDate, ok := s.LatestDates[m.MeterID]; ok && !lastDate.IsZero() {
			fromDate = lastDate
		} else {
			envKey := "START_" + m.MeterID
			if t := config.ParseDateEnv(envKey, loc); t != nil {
				fromDate = *t
			} else {
				if t := config.ParseDateEnv("START_DATE", loc); t != nil {
					fromDate = *t
				} else {
					fromDate = yesterday
				}
			}
		}
		
		if fromDate.After(yesterday) {
			log.Printf("Meter %s already up to date (%s). Skipping.", m.MeterID, s.LatestDates[m.MeterID].Format("02.01.2006"))
			continue
		}
		
		fromStr := fromDate.Format("02.01.2006")
		log.Printf("Selecting meter %s (%s) and requesting data from %s to %s...", m.MeterID, m.Label, fromStr, todayStr)

		var radioVal string
		var nodes []*cdp.Node
		err := chromedp.Run(ctx,
			chromedp.Value("#"+m.RadioID, &radioVal, chromedp.ByID),
			chromedp.ActionFunc(func(ctx context.Context) error {
				return chromedp.Evaluate(fmt.Sprintf("selectPlant('%s')", radioVal), nil).Do(ctx)
			}),
			chromedp.Sleep(2*time.Second),
			chromedp.Click("label[for=\"myForm1:j_idt1435:grid_eval:selectedClass:1\"]", chromedp.ByQuery),
			chromedp.Sleep(1*time.Second),
			chromedp.SetValue("#myForm1\\:calendarFromRegion", fromStr, chromedp.ByQuery),
			chromedp.Sleep(1*time.Second),
			chromedp.SetValue("#myForm1\\:calendarToRegion", todayStr, chromedp.ByQuery),
			chromedp.Sleep(1*time.Second),
			chromedp.Click("#myForm1\\:btnIdA1", chromedp.ByQuery),
			chromedp.Sleep(3*time.Second),
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
				points, direction := parser.ParseCSVToPoints(file, loc)
				file.Close()
				if len(points) > 0 {
					exp.Export(m.MeterID, points, direction, s, stateFilePath)
				}
			}
			os.Remove(path)
		}
	}
}
