package main

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"net/http/cgi"
	"net/smtp"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/charge"
	"github.com/stripe/stripe-go/customer"
)

type productinfo interface {
	amount(int) int64
	description(int) string
	typename() string
	thankyou(int) string
	message() string
}

var stateRE = regexp.MustCompile(`(?i)^[a-z][a-z]$`)
var zipRE = regexp.MustCompile(`^[0-9]{5}(?:-[0-9]{4})?$`)

var threads sync.WaitGroup
var orderNumberFile string
var orderLogFile string

func main() {
	http.Handle("/backend/to-stripe", http.HandlerFunc(handler))
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
	case "/home/scholacantorum/scholacantorum.org/backend":
		stripe.Key = stripeLiveSecretKey
		orderNumberFile = "/home/scholacantorum/order-number"
		orderLogFile = "/home/scholacantorum/order-log"
	case "/home/scholacantorum/new.scholacantorum.org/backend":
		stripe.Key = stripeTestSecretKey
		orderNumberFile = "/home/scholacantorum/test-order-number"
		orderLogFile = "/home/scholacantorum/test-order-log"
	default:
		fmt.Fprintf(os.Stderr, "ERROR: backend/to-stripe run from unrecognized directory\n")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Step 1.  Generate an order number.
	var onum int
	var err error
	if onum, err = nextOrderNumber(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Step 2.  Gather the order data and log it.
	var name, email, address, city, state, zip, product, qtystr, token string
	name = strings.TrimSpace(r.FormValue("name"))
	email = strings.TrimSpace(r.FormValue("email"))
	address = strings.TrimSpace(r.FormValue("address"))
	city = strings.TrimSpace(r.FormValue("city"))
	state = strings.TrimSpace(r.FormValue("state"))
	zip = strings.TrimSpace(r.FormValue("zip"))
	product = r.FormValue("product")
	qtystr = r.FormValue("qty")
	token = r.FormValue("token")
	if err = logorder(onum, name, email, address, city, state, zip, product, qtystr, token); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Step 3.  Sanity check the order data.
	var qty int
	var pi productinfo
	var ok bool
	if name == "" {
		sendError(w, onum, "Please supply your name.")
		return
	}
	if email == "" {
		sendError(w, onum, "Please supply your email address.")
		return
	}
	if address == "" {
		sendError(w, onum, "Please supply your billing address.")
		return
	}
	if city == "" {
		sendError(w, onum, "Please supply your billing address city.")
		return
	}
	if !stateRE.MatchString(state) {
		sendError(w, onum, "Please provide your billing address state as a two-letter code.")
		return
	}
	if !zipRE.MatchString(zip) {
		sendError(w, onum, "Please provide your billing zip code (5 or 9 digits).")
		return
	}
	if token == "" {
		sendError(w, onum, "Your payment card information was not correctly received.")
		return
	}
	if pi, ok = products[product]; !ok {
		sendError(w, onum, "Details of your order were not correctly received.")
		return
	}
	if qty, _ = strconv.Atoi(qtystr); qty <= 0 {
		sendError(w, onum, "Details of your order were not correctly received.")
		return
	}

	// Step 4.  Find or create the customer in Stripe.
	clistp := new(stripe.CustomerListParams)
	clistp.Filters.AddFilter("email", "", email)
	iter := customer.List(clistp)
	var cust *stripe.Customer
	for iter.Next() {
		c := iter.Customer()
		if c.Description == name && c.Shipping == nil && c.Shipping.Name == name && c.Shipping.Address.Line1 == address &&
			c.Shipping.Address.Line2 == "" && c.Shipping.Address.City == city && c.Shipping.Address.State == state &&
			c.Shipping.Address.PostalCode == zip {
			cust = c
			break
		}
	}
	if cust == nil {
		cust, err = customer.New(&stripe.CustomerParams{
			Description: &name, Email: &email, Shipping: &stripe.CustomerShippingDetailsParams{
				Name: &name, Address: &stripe.AddressParams{
					Line1: &address, City: &city, State: &state, PostalCode: &zip,
				},
			},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: stripe create customer for order %d: %s\n", onum, err)
			sendError(w, onum, "Our payment processor is currently offline.")
			return
		}
	}

	// Step 5.  Process the payment in Stripe.
	var chrg *stripe.Charge
	chrgp := &stripe.ChargeParams{
		Amount:      stripe.Int64(pi.amount(qty)),
		Currency:    stripe.String(string(stripe.CurrencyUSD)),
		Customer:    &cust.ID,
		Description: stripe.String(pi.description(qty)),
		Params: stripe.Params{
			Metadata: map[string]string{
				"order-number": strconv.Itoa(onum),
				"product":      product,
				"qty":          strconv.Itoa(qty),
			},
		},
	}
	chrgp.SetSource(token)
	chrg, err = charge.New(chrgp)
	if serr, ok := err.(*stripe.Error); ok {
		if serr.Type == stripe.ErrorTypeCard {
			sendError(w, onum, serr.Msg)
			return
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: stripe process payment for order %d: %s\n", onum, err)
		sendError(w, onum, "Our payment processor is currently offline.")
		return
	}
	if fh, err := os.OpenFile(orderLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err == nil {
		fmt.Fprintf(fh, "%d => SUCCESS, charge %s\n", onum, chrg.ID)
		fh.Close()
	}

	// Step 6.  Send email confirmation (in background).
	threads.Add(1)
	go func() {
		var message bytes.Buffer
		fmt.Fprint(&message, "From: Schola Cantorum Web Site <admin@scholacantorum.org>\r\n")
		fmt.Fprintf(&message, "To: %s <%s>\r\n", name, email)
		fmt.Fprint(&message, "Reply-To: info@scholacantorum.org\r\n")
		fmt.Fprintf(&message, "Subject: Schola Cantorum %s #%d\r\n", pi.typename(), onum)
		fmt.Fprint(&message, "Content-Type: multipart/related; boundary=SCHOLA_MESSAGE_BOUNDARY\r\n")
		fmt.Fprint(&message, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		fmt.Fprint(&message, "--SCHOLA_MESSAGE_BOUNDARY\r\n")
		fmt.Fprint(&message, "Content-Type: text/html; charset=UTF-8\r\n\r\n")
		fmt.Fprint(&message, `
<!DOCTYPE html><html><body style="margin:0">
<div style="width:600px;margin:0 auto">
<div style="margin-bottom:24px"><img src="cid:SCHOLA_LOGO" alt="[Schola Cantorum]" style="border-width:0"></div>
`)
		fmt.Fprintf(&message, "<p>Greetings, %s:</p>\n", html.EscapeString(name))
		fmt.Fprintf(&message, "<p>%s</p>\n", html.EscapeString(pi.thankyou(qty)))
		fmt.Fprint(&message, pi.message())
		fmt.Fprint(&message, `
<p>Sincerely yours,<br>Schola Cantorum</p>
<p>Web site: <a href="https://scholacantorum.org">scholacantorum.org</a><br>
Email: <a href="mailto:info@scholacantorum.org">info@scholacantorum.org</a><br>
Phone: 650-254-1700</p></body></html>
`)
		fmt.Fprint(&message, "--SCHOLA_MESSAGE_BOUNDARY\r\n")
		fmt.Fprint(&message, "Content-Type: image/gif\r\n")
		fmt.Fprint(&message, "Content-Transfer-Encoding: base64\r\n")
		fmt.Fprint(&message, "Content-ID: <SCHOLA_LOGO>\r\n\r\n")
		fmt.Fprint(&message, mailLogo)
		fmt.Fprint(&message, "--SCHOLA_MESSAGE_BOUNDARY--\r\n")
		if err = smtp.SendMail("smtp.gmail.com:587",
			smtp.PlainAuth("", "admin@scholacantorum.org", "3ayoP4vEfkLw", "smtp.gmail.com"),
			"admin@scholacantorum.org",
			[]string{email, "admin@scholacantorum.org", "info@scholacantorum.org"},
			// []string{email, "admin@scholacantorum.org"},
			message.Bytes()); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: can't send email for order %d: %s\n", onum, err)
		}
		threads.Done()
	}()

	// Step 7.  Report success.
	w.WriteHeader(http.StatusOK)
}

func nextOrderNumber() (onum int, err error) {
	var fh *os.File

	if fh, err = os.OpenFile(orderNumberFile, os.O_RDWR, 0); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: order-number: %s\n", err)
		return 0, err
	}
	if err = syscall.Flock(int(fh.Fd()), syscall.LOCK_EX); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: order-number: %s\n", err)
		return 0, err
	}
	if _, err = fmt.Fscanln(fh, &onum); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: order-number: %s\n", err)
		return 0, err
	}
	onum++
	if _, err = fh.Seek(0, os.SEEK_SET); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: order-number: %s\n", err)
		return 0, err
	}
	if _, err = fmt.Fprintln(fh, onum); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: order-number: %s\n", err)
		return 0, err
	}
	if err = fh.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: order-number: %s\n", err)
		return 0, err
	}
	return onum, nil
}

func logorder(onum int, name, email, address, city, state, zip, product, qtystr, token string) (err error) {
	var fh *os.File

	if fh, err = os.OpenFile(orderLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: open order-log: %s\n", err)
		return err
	}
	if _, err = fmt.Fprintf(fh, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		onum, name, email, address, city, state, zip, product, qtystr, token); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: append order-log: %s %d\n", err, fh.Fd())
		return err
	}
	if err = fh.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: order-log: %s\n", err)
		return err
	}
	return nil
}

func sendError(w http.ResponseWriter, onum int, message string) {
	var fh *os.File
	var err error

	if fh, err = os.OpenFile(orderLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err == nil {
		fmt.Fprintf(fh, "%d => ERROR %s\n", onum, message)
		fh.Close()
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprint(w, message)
}
