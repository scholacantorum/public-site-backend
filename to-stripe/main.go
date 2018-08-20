package main

import (
	"bytes"
	"encoding/json"
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
	"time"

	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/customer"
	sorder "github.com/stripe/stripe-go/order"
)

type orderinfo struct {
	Timestamp   time.Time
	OrderNumber int
	Name        string `json:",omitempty"`
	Email       string `json:",omitempty"`
	Address     string `json:",omitempty"`
	City        string `json:",omitempty"`
	State       string `json:",omitempty"`
	Zip         string `json:",omitempty"`
	Product     string `json:",omitempty"`
	Quantity    int    `json:",omitempty"`
	Donation    int    `json:",omitempty"`
	Coupon      string `json:",omitempty"`
	Total       int64  `json:",omitempty"`
	PaySource   string `json:",omitempty"`
	CustomerID  string `json:",omitempty"`
	OrderID     string `json:",omitempty"`
	ChargeID    string `json:",omitempty"`
	Error       string `json:",omitempty"`
	itempi      productinfo
	couponpi    productinfo
}

type productinfo interface {
	amount(int) int64
	description(int) string
	thankyou(int) string
	message() string
}

var stateRE = regexp.MustCompile(`(?i)^[a-z][a-z]$`)
var zipRE = regexp.MustCompile(`^[0-9]{5}(?:-[0-9]{4})?$`)

var threads sync.WaitGroup
var orderNumberFile string
var orderLogFile string
var emailTo []string
var order orderinfo

func main() {
	http.Handle("/backend/to-stripe", http.HandlerFunc(handler))
	cgi.Serve(nil)
	threads.Wait()
}

func handler(w http.ResponseWriter, r *http.Request) {
	if !checkRequestMethod(w, r) {
		return
	}
	if !getMode(w) {
		return
	}
	if !getOrderNumber(w) {
		return
	}
	order.Timestamp = time.Now()
	defer logOrder()
	if !getOrderData(w, r) {
		return
	}
	if !validateOrderData(w) {
		return
	}
	if !findOrCreateCustomer(w) {
		return
	}
	if !createOrder(w) {
		return
	}
	if !payOrder(w) {
		cancelOrder()
		return
	}
	threads.Add(1)
	go sendEmail()
	w.WriteHeader(http.StatusOK)
}

func checkRequestMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func getMode(w http.ResponseWriter) bool {
	switch cwd, _ := os.Getwd(); cwd {
	case "/home/scholacantorum/scholacantorum.org/backend":
		stripe.Key = stripeLiveSecretKey
		orderNumberFile = "/home/scholacantorum/order-number"
		orderLogFile = "/home/scholacantorum/order-log"
		emailTo = []string{"info@scholacantorum.org", "admin@scholacantorum.org"}
	case "/home/scholacantorum/new.scholacantorum.org/backend":
		stripe.Key = stripeTestSecretKey
		orderNumberFile = "/home/scholacantorum/test-order-number"
		orderLogFile = "/home/scholacantorum/test-order-log"
		emailTo = []string{"admin@scholacantorum.org"}
	default:
		fmt.Fprintf(os.Stderr, "ERROR: backend/to-stripe run from unrecognized directory\n")
		w.WriteHeader(http.StatusInternalServerError)
		return false
	}
	return true
}

func getOrderNumber(w http.ResponseWriter) bool {
	var fh *os.File
	var err error

	if fh, err = os.OpenFile(orderNumberFile, os.O_RDWR, 0); err != nil {
		goto ERROR
	}
	if err = syscall.Flock(int(fh.Fd()), syscall.LOCK_EX); err != nil {
		goto ERROR
	}
	if _, err = fmt.Fscanln(fh, &order.OrderNumber); err != nil {
		goto ERROR
	}
	order.OrderNumber++
	if _, err = fh.Seek(0, os.SEEK_SET); err != nil {
		goto ERROR
	}
	if _, err = fmt.Fprintln(fh, order.OrderNumber); err != nil {
		goto ERROR
	}
	if err = fh.Close(); err != nil {
		goto ERROR
	}
	return true
ERROR:
	fmt.Fprintf(os.Stderr, "ERROR: order-number: %s\n", err)
	w.WriteHeader(http.StatusInternalServerError)
	return false
}

func logOrder() {
	var fh *os.File
	var enc *json.Encoder
	var err error

	if fh, err = os.OpenFile(orderLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: open order-log: %s\n", err)
		return
	}
	enc = json.NewEncoder(fh)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "")
	if err = enc.Encode(&order); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: write order-log: %s\n", err)
		return
	}
	if err = fh.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: order-log: %s\n", err)
		return
	}
}

func getOrderData(w http.ResponseWriter, r *http.Request) bool {
	var err error
	if err = json.NewDecoder(r.Body).Decode(&order); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: request body JSON: %s\n", err)
		sendError(w, "Details of your order were not correctly received.")
		return false
	}
	order.Timestamp = time.Now()
	return true
}

func validateOrderData(w http.ResponseWriter) bool {
	order.Name = strings.TrimSpace(order.Name)
	if order.Name == "" {
		sendError(w, "Please supply your name.")
		return false
	}
	order.Email = strings.TrimSpace(order.Email)
	if order.Email == "" {
		sendError(w, "Please supply your email address.")
		return false
	}
	order.Address = strings.TrimSpace(order.Address)
	if order.Address == "" {
		sendError(w, "Please supply your billing address.")
		return false
	}
	order.City = strings.TrimSpace(order.City)
	if order.City == "" {
		sendError(w, "Please supply your billing address city.")
		return false
	}
	order.State = strings.TrimSpace(order.State)
	if !stateRE.MatchString(order.State) {
		sendError(w, "Please provide your billing address state as a two-letter code.")
		return false
	}
	order.Zip = strings.TrimSpace(order.Zip)
	if !zipRE.MatchString(order.Zip) {
		sendError(w, "Please provide your billing zip code (5 or 9 digits).")
		return false
	}
	if order.Quantity < 0 || (order.Quantity == 0 && order.Product != "") {
		sendError(w, "")
		return false
	}
	if order.Donation < 0 || (order.Donation == 0 && order.Product == "") {
		sendError(w, "")
		return false
	}
	if order.itempi = products[order.Product]; order.Quantity > 0 && order.itempi == nil {
		sendError(w, "")
		return false
	}
	order.Coupon = strings.ToUpper(strings.TrimSpace(order.Coupon))
	if order.Coupon != "" {
		if order.couponpi = products[order.Product+"_"+order.Coupon]; order.couponpi == nil {
			sendError(w, "The coupon code is not recognized.")
			return false
		}
	}
	// Note that verification of the client's total against ours happens
	// later, during order creation.
	if order.PaySource == "" {
		sendError(w, "")
		return false
	}
	return true
}

func findOrCreateCustomer(w http.ResponseWriter) bool {
	var clistp *stripe.CustomerListParams
	var iter *customer.Iter
	var cust *stripe.Customer
	var err error

	clistp = new(stripe.CustomerListParams)
	clistp.Filters.AddFilter("email", "", order.Email)
	iter = customer.List(clistp)
	for iter.Next() {
		c := iter.Customer()
		if c.Description == order.Name && c.Shipping == nil && c.Shipping.Name == order.Name &&
			c.Shipping.Address.Line1 == order.Address && c.Shipping.Address.Line2 == "" &&
			c.Shipping.Address.City == order.City && c.Shipping.Address.State == order.State &&
			c.Shipping.Address.PostalCode == order.Zip {
			cust = c
			break
		}
	}
	if cust == nil {
		cust, err = customer.New(&stripe.CustomerParams{
			Description: &order.Name, Email: &order.Email, Shipping: &stripe.CustomerShippingDetailsParams{
				Name: &order.Name, Address: &stripe.AddressParams{
					Line1: &order.Address, City: &order.City, State: &order.State, PostalCode: &order.Zip,
				},
			},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: stripe create customer for order %d: %s\n", order.OrderNumber, err)
			sendError(w, "")
			return false
		}
	}
	order.CustomerID = cust.ID
	return true
}

func createOrder(w http.ResponseWriter) bool {
	var params *stripe.OrderParams
	var o *stripe.Order
	var err error

	params = &stripe.OrderParams{
		Currency: stripe.String(string(stripe.CurrencyUSD)),
		Customer: &order.CustomerID,
		Params: stripe.Params{
			Metadata: map[string]string{
				"order-number": strconv.Itoa(order.OrderNumber),
			},
		},
	}
	if order.Quantity > 0 {
		params.Items = append(params.Items, &stripe.OrderItemParams{
			Amount:      stripe.Int64(order.itempi.amount(order.Quantity)),
			Currency:    stripe.String(string(stripe.CurrencyUSD)),
			Description: stripe.String(order.itempi.description(order.Quantity)),
			Parent:      &order.Product,
			Quantity:    stripe.Int64(int64(order.Quantity)),
			Type:        stripe.String(string(stripe.OrderItemTypeSKU)),
		})
		if order.couponpi != nil {
			params.Items = append(params.Items, &stripe.OrderItemParams{
				Amount:      stripe.Int64(order.couponpi.amount(order.Quantity)),
				Currency:    stripe.String(string(stripe.CurrencyUSD)),
				Description: stripe.String(order.couponpi.description(order.Quantity)),
				Quantity:    stripe.Int64(int64(order.Quantity)),
				Type:        stripe.String(string(stripe.OrderItemTypeDiscount)),
			})
		}
	}
	if order.Donation > 0 {
		params.Items = append(params.Items, &stripe.OrderItemParams{
			Amount:      stripe.Int64(int64(order.Donation) * 100),
			Currency:    stripe.String(string(stripe.CurrencyUSD)),
			Description: stripe.String("Tax-Deductible Donation"),
			Parent:      stripe.String("donation"),
			Quantity:    stripe.Int64(int64(order.Donation)),
			Type:        stripe.String(string(stripe.OrderItemTypeSKU)),
		})
	}
	if o, err = sorder.New(params); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: stripe create order %d: %s", order.OrderNumber, err)
		sendError(w, "")
		return false
	}
	order.OrderID = o.ID
	return true
}

func payOrder(w http.ResponseWriter) bool {
	var params *stripe.OrderPayParams
	var o *stripe.Order
	var err error

	params = new(stripe.OrderPayParams)
	params.SetSource(order.PaySource)
	o, err = sorder.Pay(order.OrderID, params)
	if serr, ok := err.(*stripe.Error); ok {
		if serr.Type == stripe.ErrorTypeCard {
			sendError(w, serr.Msg)
			return false
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: stripe pay order %d: %s", order.OrderNumber, err)
		sendError(w, "")
		return false
	}
	order.ChargeID = o.Charge.ID
	return true
}

func cancelOrder() {
	var params *stripe.OrderUpdateParams
	var err error

	params = &stripe.OrderUpdateParams{
		Status: stripe.String(string(stripe.OrderStatusCanceled)),
	}
	if _, err = sorder.Update(order.OrderID, params); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: stripe cancel order %d: %s\n", order.OrderNumber, err)
	}
}

func sendEmail() {
	var message bytes.Buffer
	var typename string
	var thankyou string
	var err error

	if order.itempi != nil {
		typename = "Order"
		thankyou = order.itempi.thankyou(order.Quantity)
		thankyou = fmt.Sprintf("%s, and for your generous donation of $%d.", thankyou[:len(thankyou)-1], order.Donation)
	} else {
		typename = "Donation"
		thankyou = fmt.Sprintf("Thank you for your generous donation of $%d.", order.Donation)
	}
	fmt.Fprint(&message, "From: Schola Cantorum Web Site <admin@scholacantorum.org>\r\n")
	fmt.Fprintf(&message, "To: %s <%s>\r\n", order.Name, order.Email)
	fmt.Fprint(&message, "Reply-To: info@scholacantorum.org\r\n")
	fmt.Fprintf(&message, "Subject: Schola Cantorum %s #%d\r\n", typename, order.OrderNumber)
	fmt.Fprint(&message, "Content-Type: multipart/related; boundary=SCHOLA_MESSAGE_BOUNDARY\r\n")
	fmt.Fprint(&message, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	fmt.Fprint(&message, "--SCHOLA_MESSAGE_BOUNDARY\r\n")
	fmt.Fprint(&message, "Content-Type: text/html; charset=UTF-8\r\n\r\n")
	fmt.Fprint(&message, `
<!DOCTYPE html><html><body style="margin:0">
<div style="width:600px;margin:0 auto">
<div style="margin-bottom:24px"><img src="cid:SCHOLA_LOGO" alt="[Schola Cantorum]" style="border-width:0"></div>
`)
	fmt.Fprintf(&message, "<p>Greetings, %s:</p>\n", html.EscapeString(order.Name))
	fmt.Fprintf(&message, "<p>%s</p>\n", html.EscapeString(thankyou))
	if order.itempi != nil {
		fmt.Fprint(&message, order.itempi.message())
	}
	if order.Donation > 0 {
		fmt.Fprint(&message, `
<p>Your donation is tax-deductible. Schola Cantorum’s tax ID number is 94-2597822.
A confirmation letter will be mailed to the billing address you provided.</p>
`)
	}
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
	emailTo = append(emailTo, order.Email)
	if err = smtp.SendMail("smtp.gmail.com:587",
		smtp.PlainAuth("", "admin@scholacantorum.org", "3ayoP4vEfkLw", "smtp.gmail.com"),
		"admin@scholacantorum.org", emailTo, message.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: can't send email for order %d: %s\n", order.OrderNumber, err)
	}
	threads.Done()
}

func sendError(w http.ResponseWriter, message string) {
	if message == "" {
		message = "We’re sorry: we’re unable to process your payment at this time.  Please try again later, or call our office at (650) 254-1700."
	}
	order.Error = message
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprint(w, message)
}
