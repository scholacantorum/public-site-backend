package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/scholacantorum/public-site-backend/backend-log"
	"github.com/scholacantorum/public-site-backend/private"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/order"
)

var threads sync.WaitGroup
var toaddr string
var toaddrs []string

func main() {
	belog.LogApp = "mail-signup"
	defer func() {
		if err := recover(); err != nil {
			belog.Log("PANIC: %s", err)
			panic(err)
		}
	}()
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
		stripe.Key = private.StripeLiveSecretKey
		belog.LogMode = "LIVE"
	case "/home/scholacantorum/new.scholacantorum.org/backend":
		toaddr = "admin@scholacantorum.org"
		toaddrs = []string{"admin@scholacantorum.org"}
		stripe.Key = private.StripeTestSecretKey
		belog.LogMode = "TEST"
	default:
		belog.Log("run from unrecognized directory")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Step 1.  Gather data.
	var orderid, name, address, city, state, zip string
	if orderid = strings.TrimSpace(r.FormValue("order")); orderid != "" {
		var o *stripe.Order
		var err error

		if o, err = order.Get(orderid, nil); err != nil {
			belog.Log("getting order %s: %s", orderid, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if o.Shipping == nil {
			belog.Log("order %s had no shipping", orderid)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		name = o.Shipping.Name
		address = o.Shipping.Address.Line1
		city = o.Shipping.Address.City
		state = o.Shipping.Address.State
		zip = o.Shipping.Address.PostalCode
	} else {
		name = strings.TrimSpace(r.FormValue("name"))
		address = strings.TrimSpace(r.FormValue("address"))
		city = strings.TrimSpace(r.FormValue("city"))
		state = strings.TrimSpace(r.FormValue("state"))
		zip = strings.TrimSpace(r.FormValue("zip"))
	}

	// Step 2.  Check the robot trap.
	if r.FormValue("b_4eefbbf83086ccdfdac86e1c3_5df4425cfb") != "" {
		// A human wouldn't fill anything into that field, because it's
		// invisible.  Must be a robot.  Ignore.
		w.WriteHeader(http.StatusOK)
		return
	}

	// Step 3.  Send email to office (in background).
	var cmd *exec.Cmd
	var pipe io.WriteCloser
	var err error
	cmd = exec.Command("/home/scholacantorum/bin/send-email", toaddrs...)
	if pipe, err = cmd.StdinPipe(); err != nil {
		belog.Log("can't pipe to send-email: %s", err)
		return
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		belog.Log("can't start send-email: %s", err)
		return
	}
	fmt.Fprintf(pipe, `From: Schola Cantorum Web Site <admin@scholacantorum.org>
To: %s
Subject: Mailing List Request

<p>On the Schola Cantorum web site, we have received a request to add</p>
<pre>%s
%s
%s, %s  %s</pre>
<p>to our postal mail list.</p>
<p>Regards,<br>The Web Site</p>
`, toaddr, name, address, city, state, zip)
	pipe.Close()

	// Step 4.  Report success.
	w.WriteHeader(http.StatusOK)
}
