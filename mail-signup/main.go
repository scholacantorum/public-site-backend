package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/smtp"
	"os"
	"strings"
	"sync"

	"github.com/scholacantorum/public-site-backend/private"
)

var threads sync.WaitGroup
var toaddr string
var toaddrs []string

func main() {
	http.Handle("/backend/mail-signup", http.HandlerFunc(handler))
	cgi.Serve(nil)
	threads.Wait()
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Step 0.  Determine live mode vs. test mode.
	switch cwd, _ := os.Getwd(); cwd {
	case "/home/scholacantorum/scholacantorum.org/backend", "/home/scholacantorum/scholacantorum.org/public/backend":
		toaddr = "info@scholacantorum.org"
		toaddrs = []string{"info@scholacantorum.org", "admin@scholacantorum.org"}
	case "/home/scholacantorum/new.scholacantorum.org/backend":
		toaddr = "admin@scholacantorum.org"
		toaddrs = []string{"admin@scholacantorum.org"}
	default:
		fmt.Fprintf(os.Stderr, "ERROR: backend/mail-signup run from unrecognized directory\n")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Step 1.  Gather data.
	name := strings.TrimSpace(r.FormValue("name"))
	address := strings.TrimSpace(r.FormValue("address"))
	city := strings.TrimSpace(r.FormValue("city"))
	state := strings.TrimSpace(r.FormValue("state"))
	zip := strings.TrimSpace(r.FormValue("zip"))

	// Step 2.  Check the robot trap.
	if r.FormValue("b_4eefbbf83086ccdfdac86e1c3_5df4425cfb") != "" {
		// A human wouldn't fill anything into that field, because it's
		// invisible.  Must be a robot.  Ignore.
		w.WriteHeader(http.StatusOK)
		return
	}

	// Step 3.  Send email to office (in background).
	threads.Add(1)
	go func() {
		var message bytes.Buffer
		fmt.Fprint(&message, "From: Schola Cantorum Web Site <admin@scholacantorum.org>\r\n")
		fmt.Fprintf(&message, "To: %s\r\n", toaddr)
		fmt.Fprint(&message, "Subject: Mailing List Request\r\n\r\n")
		fmt.Fprintf(&message, `
On the Schola Cantorum web site, we have received a request to add

%s
%s
%s, %s %s

to our postal mail list.

Regards,
The Web Site
`, name, address, city, state, zip)
		if err := smtp.SendMail(private.SMTPServer,
			smtp.PlainAuth("", private.SMTPUsername, private.SMTPPassword, private.SMTPHost),
			"admin@scholacantorum.org", toaddrs, message.Bytes()); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: can't send email for mailing list request: %s\n", err)
		}
		threads.Done()
	}()

	// Step 4.  Report success.
	w.WriteHeader(http.StatusOK)
}
