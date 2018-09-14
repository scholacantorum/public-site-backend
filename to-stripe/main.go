package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/scholacantorum/public-site-backend/backend-log"
	"github.com/scholacantorum/public-site-backend/private"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/customer"
	sorder "github.com/stripe/stripe-go/order"
	"github.com/stripe/stripe-go/sku"
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
	Count       int    `json:",omitempty"`
	Coupon      string `json:",omitempty"`
	Total       int64  `json:",omitempty"`
	PayType     string `json:",omitempty"`
	PaySource   string `json:",omitempty"`
	CustomerID  string `json:",omitempty"`
	OrderID     string `json:",omitempty"`
	ChargeID    string `json:",omitempty"`
	Error       string `json:",omitempty"`
	sku         *stripe.SKU
}

var stateRE = regexp.MustCompile(`(?i)^[a-z][a-z]$`)
var zipRE = regexp.MustCompile(`^[0-9]{5}(?:-[0-9]{4})?$`)

var orderNumberFile string
var orderLogFile string
var emailTo []string
var order orderinfo

func main() {
	belog.LogApp = "to-stripe"
	defer func() {
		if err := recover(); err != nil {
			belog.Log("PANIC: %s", err)
			panic(err)
		}
	}()
	http.Handle("/backend/to-stripe", http.HandlerFunc(handler))
	cgi.Serve(nil)
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
	sendEmail()
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(order.OrderID))
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
	case "/home/scholacantorum/scholacantorum.org/backend", "/home/scholacantorum/scholacantorum.org/public/backend":
		stripe.Key = private.StripeLiveSecretKey
		orderNumberFile = "/home/scholacantorum/order-number"
		orderLogFile = "/home/scholacantorum/order-log"
		emailTo = []string{"info@scholacantorum.org", "admin@scholacantorum.org"}
		belog.LogMode = "LIVE"
	case "/home/scholacantorum/new.scholacantorum.org/backend":
		stripe.Key = private.StripeTestSecretKey
		orderNumberFile = "/home/scholacantorum/test-order-number"
		orderLogFile = "/home/scholacantorum/test-order-log"
		emailTo = []string{"admin@scholacantorum.org"}
		belog.LogMode = "TEST"
	default:
		belog.Log("run from unrecognized directory %s", cwd)
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
	belog.Log("order-number: %s", err)
	w.WriteHeader(http.StatusInternalServerError)
	return false
}

func logOrder() {
	var fh *os.File
	var enc *json.Encoder
	var err error

	if fh, err = os.OpenFile(orderLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err != nil {
		belog.Log("open order-log: %s", err)
		return
	}
	enc = json.NewEncoder(fh)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "")
	if err = enc.Encode(&order); err != nil {
		belog.Log("write order-log: %s", err)
		return
	}
	if err = fh.Close(); err != nil {
		belog.Log("order-log: %s", err)
		return
	}
}

func getOrderData(w http.ResponseWriter, r *http.Request) bool {
	var err error
	if err = json.NewDecoder(r.Body).Decode(&order); err != nil {
		belog.Log("request body JSON: %s", err)
		sendError(w, "Details of your order were not correctly received.")
		return false
	}
	order.Timestamp = time.Now()
	return true
}

func validateOrderData(w http.ResponseWriter) bool {
	var err error

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
	order.City = strings.TrimSpace(order.City)
	order.State = strings.TrimSpace(order.State)
	if order.State != "" && !stateRE.MatchString(order.State) {
		sendError(w, "Please provide your billing address state as a two-letter code.")
		return false
	}
	order.Zip = strings.TrimSpace(order.Zip)
	if order.Zip != "" && !zipRE.MatchString(order.Zip) {
		sendError(w, "Please provide your billing zip code in 5- or 9-digit format.")
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
	if order.Product != "" {
		if order.sku, err = getSKU(order.Product); err != nil {
			sendError(w, "")
			return false
		}
		order.Coupon = strings.ToUpper(strings.TrimSpace(order.Coupon))
		if order.Coupon != order.sku.Attributes["coupon"] {
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

func getSKU(id string) (s *stripe.SKU, err error) {
	pp := stripe.SKUParams{}
	pp.AddExpand("product")
	if s, err = sku.Get(id, &pp); err != nil {
		return nil, err
	}
	if s.Attributes["coupon"] == "-" {
		s.Attributes["coupon"] = ""
	} else {
		s.Attributes["coupon"] = strings.ToUpper(s.Attributes["coupon"])
	}
	return s, nil
}

func findOrCreateCustomer(w http.ResponseWriter) bool {
	var cust *stripe.Customer
	var err error

	if order.Count == 0 {
		// This is a one-off order, not a monthly donation.  Look for
		// an existing customer entry we can use.  (We don't reuse
		// customer entries for monthly donations, so that each one can
		// have its own preserved payment source, and so that it's
		// easier to find the donation orders corresponding to a
		// particular donation sequence.)

		var clistp *stripe.CustomerListParams
		var iter *customer.Iter

		clistp = new(stripe.CustomerListParams)
		clistp.Filters.AddFilter("email", "", order.Email)
		iter = customer.List(clistp)
		for iter.Next() {
			c := iter.Customer()
			if c.Description != order.Name || c.Email != order.Email {
				continue
			}
			cust = c

			// Update the customer with the metadata and payment
			// source for the new order.
			var cparams = new(stripe.CustomerParams)
			cparams.SetSource(order.PaySource)
			if c, err = customer.Update(c.ID, cparams); err != nil {
				if serr, ok := err.(*stripe.Error); ok {
					if serr.Type == stripe.ErrorTypeCard {
						sendError(w, serr.Msg)
						return false
					}
				}
				belog.Log("stripe update customer for order %d: %s", order.OrderNumber, err)
				sendError(w, "")
				return false
			}
			break
		}
	}

	if cust == nil { // Didn't find an existing customer (or it's a new
		// monthly donation).  Create a customer.

		var cparams = stripe.CustomerParams{Description: &order.Name, Email: &order.Email}
		cparams.SetSource(order.PaySource)
		if order.Count != 0 { // monthly donation
			cparams.Params.Metadata = map[string]string{
				"monthly-donation-amount": strconv.Itoa(order.Donation),
				"monthly-donation-count":  strconv.Itoa(order.Count),
				"monthly-donation-start":  order.Timestamp.Format(time.RFC3339),
			}
		}
		cust, err = customer.New(&cparams)
		if serr, ok := err.(*stripe.Error); ok {
			if serr.Type == stripe.ErrorTypeCard {
				sendError(w, serr.Msg)
				return false
			}
		}
		if err != nil {
			belog.Log("stripe create customer for order %d: %s", order.OrderNumber, err)
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
		Email:    &order.Email,
		Params: stripe.Params{
			Metadata: map[string]string{
				"order-number": strconv.Itoa(order.OrderNumber),
			},
		},
	}
	if order.PayType != "" {
		params.Params.Metadata["payment-type"] = order.PayType
	}
	if order.Address != "" {
		params.Shipping = &stripe.ShippingParams{
			Name: &order.Name,
			Address: &stripe.AddressParams{
				Line1:      &order.Address,
				City:       &order.City,
				State:      &order.State,
				PostalCode: &order.Zip,
			},
		}
	}
	if order.Quantity > 0 {
		params.Items = append(params.Items, &stripe.OrderItemParams{
			Amount:      stripe.Int64(int64(order.Quantity) * order.sku.Price),
			Currency:    stripe.String(string(stripe.CurrencyUSD)),
			Description: stripe.String(order.sku.Product.Name),
			Parent:      &order.Product,
			Quantity:    stripe.Int64(int64(order.Quantity)),
			Type:        stripe.String(string(stripe.OrderItemTypeSKU)),
		})
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
		belog.Log("stripe create order %d: %s", order.OrderNumber, err)
		sendError(w, "")
		return false
	}
	order.OrderID = o.ID
	return true
}

func payOrder(w http.ResponseWriter) bool {
	var o *stripe.Order
	var err error

	o, err = sorder.Pay(order.OrderID, &stripe.OrderPayParams{Customer: &order.CustomerID})
	if serr, ok := err.(*stripe.Error); ok {
		if serr.Type == stripe.ErrorTypeCard {
			sendError(w, serr.Msg)
			return false
		}
	}
	if err != nil {
		belog.Log("stripe pay order %d: %s", order.OrderNumber, err)
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
		belog.Log("stripe cancel order %d: %s", order.OrderNumber, err)
	}
}

func sendEmail() {
	var cmd *exec.Cmd
	var typename string
	var prodtext []byte
	var dontext []byte
	var pipe io.WriteCloser
	var err error

	emailTo = append(emailTo, order.Email)
	cmd = exec.Command("/home/scholacantorum/bin/send-email", emailTo...)
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

	if order.sku != nil {
		typename = "Order"
		if prodtext, err = ioutil.ReadFile(filepath.Join("../confirms", order.sku.Product.ID, "index.html")); err != nil {
			belog.Log("%s", err)
			return
		}
	} else {
		typename = "Donation"
	}
	if order.Donation > 0 {
		if dontext, err = ioutil.ReadFile(filepath.Join("../confirms", "donation", "index.html")); err != nil {
			belog.Log("%s", err)
			return
		}
	}
	fmt.Fprintf(pipe, `From: Schola Cantorum Web Site <admin@scholacantorum.org>
To: %s <%s>
Reply-To: info@scholacantorum.org
Subject: Schola Cantorum %s #%d

<p>Dear %s,</p>
`, order.Name, order.Email, typename, order.OrderNumber, html.EscapeString(order.Name))

	if prodtext != nil {
		prodtext = bytes.Replace(prodtext, []byte("PRICE"), []byte(strconv.Itoa(int(order.sku.Price/100))), -1)
		if order.Quantity == 1 {
			prodtext = bytes.Replace(prodtext, []byte("QTY"), []byte("one"), -1)
			prodtext = bytes.Replace(prodtext, []byte("(S)"), []byte{}, -1)
			prodtext = bytes.Replace(prodtext, []byte("(ES)"), []byte{}, -1)
			prodtext = bytes.Replace(prodtext, []byte("_EACH"), []byte{}, -1)
		} else {
			prodtext = bytes.Replace(prodtext, []byte("QTY"), []byte(strconv.Itoa(order.Quantity)), -1)
			prodtext = bytes.Replace(prodtext, []byte("(S)"), []byte{'s'}, -1)
			prodtext = bytes.Replace(prodtext, []byte("(ES)"), []byte{'e', 's'}, -1)
			prodtext = bytes.Replace(prodtext, []byte("_EACH"), []byte(" each"), -1)
		}
		pipe.Write(prodtext)
	}
	if dontext != nil {
		dontext = bytes.Replace(dontext, []byte("DONATION"), []byte(strconv.Itoa(order.Donation)), -1)
		pipe.Write(dontext)
	}
	if order.Quantity > 1 || (order.Quantity == 1 && order.Donation > 0) {
		fmt.Fprintf(pipe, "<p>The total charge to your card was $%d.</p>", order.Total)
	}
	fmt.Fprintf(pipe, `<p>Sincerely yours,<br>Schola Cantorum</p><p>Web: <a href="https://scholacantorum.org">scholacantorum.org</a><br>Email: <a href="mailto:info@scholacantorum.org">info@scholacantorum.org</a><br>Phone: (650) 254-1700</p>`)
	pipe.Close()
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
