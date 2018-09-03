// email-signup is a CGI script that takes a single parameter, order, which
// is a Stripe order ID.  It emits HTML that posts the necessary form to
// MailChimp.  (It would be better to do this through their API, but I don't
// have access yet.)
package main

import (
	"fmt"
	"net/http"
	"net/http/cgi"
	"os"
	"strings"

	"github.com/scholacantorum/public-site-backend/backend-log"
	"github.com/scholacantorum/public-site-backend/private"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/order"
)

func main() {
	belog.LogApp = "email-signup"
	http.Handle("/backend/email-signup", http.HandlerFunc(handler))
	cgi.Serve(nil)
}

func handler(w http.ResponseWriter, r *http.Request) {

	// Step 0.  Determine live mode vs. test mode.
	switch cwd, _ := os.Getwd(); cwd {
	case "/home/scholacantorum/scholacantorum.org/backend", "/home/scholacantorum/scholacantorum.org/public/backend":
		belog.LogMode = "LIVE"
		stripe.Key = private.StripeLiveSecretKey
	case "/home/scholacantorum/new.scholacantorum.org/backend":
		belog.LogMode = "TEST"
		stripe.Key = private.StripeTestSecretKey
	default:
		belog.Log("run from unrecognized directory")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Step 1.  Gather data.
	oid := strings.TrimSpace(r.FormValue("order"))
	op := stripe.OrderParams{}
	op.AddExpand("customer")
	var o *stripe.Order
	var err error
	if o, err = order.Get(oid, &op); err != nil {
		belog.Log("get order %s: %s", oid, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var names = strings.SplitN(o.Customer.Description, " ", 2)
	if len(names) < 2 {
		names = append(names, "")
	}

	// Emit the page.
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<html><body>
<form id="subform" action="https://scholacantorum.us2.list-manage.com/subscribe/post?u=4eefbbf83086ccdfdac86e1c3&amp;id=5df4425cfb" method="post" novalidate>
<input type="hidden" name="FNAME" value="%s">
<input type="hidden" name="LNAME" value="%s">
<input type="hidden" name="EMAIL" value="%s">
<input type="hidden" name="EMAILTYPE" value="html">
<input type="hidden" name="b_4eefbbf83086ccdfdac86e1c3_5df4425cfb">
</form>
<script>document.getElementById('subform').submit()</script></body></html>
`, names[0], names[1], o.Email)
}
