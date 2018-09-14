package main

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/scholacantorum/public-site-backend/backend-log"
	"github.com/scholacantorum/public-site-backend/private"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/customer"
	"github.com/stripe/stripe-go/order"
)

var orderNumberFile string
var confirmfile string
var emailTo []string
var verbose = true

func main() {
	belog.LogApp = "monthly-orders"
	stripe.LogLevel = 1

	// Process monthly test orders.
	stripe.Key = private.StripeTestSecretKey
	orderNumberFile = "/home/scholacantorum/test-order-number"
	confirmfile = "/home/scholacantorum/new.scholacantorum.org/confirms/donation/index.html"
	emailTo = []string{"admin@scholacantorum.org"}
	belog.LogMode = "TEST"
	processMonthlyOrders()

	// Process monthly live orders.
	stripe.Key = private.StripeLiveSecretKey
	orderNumberFile = "/home/scholacantorum/order-number"
	confirmfile = "/home/scholacantorum/scholacantorum.org/confirms/donation/index.html"
	emailTo = []string{"info@scholacantorum.org", "admin@scholacantorum.org"}
	belog.LogMode = "LIVE"
	processMonthlyOrders()
}

func processMonthlyOrders() {
	// Walk the list of customers, looking for ones with ongoing monthly
	// orders.
	iter := customer.List(nil)
	for iter.Next() {
		if iter.Customer().Metadata["monthly-donation-count"] == "" {
			continue
		}
		processMonthlyOrder(iter.Customer())
	}
	if err := iter.Err(); err != nil {
		belog.Log("customer list: %s", err)
	}
}

func processMonthlyOrder(c *stripe.Customer) {
	var newest time.Time
	var count int
	var last bool

	if verbose {
		fmt.Println(belog.LogMode, "Checking", c.Description, c.ID)
	}

	// Get the date of the most recent paid order for this customer.  If we
	// have a limit, also get the total number of paid orders.
	iter := order.List(&stripe.OrderListParams{Customer: &c.ID})
	for iter.Next() {
		o := iter.Order()
		if o.Status != "paid" {
			continue
		}
		if newest.IsZero() {
			newest = time.Unix(o.Created, 0)
		}
		if c.Metadata["monthly-donation-count"] == "-1" {
			break
		}
		count++
	}
	if err := iter.Err(); err != nil {
		belog.Log("order list for %s: %s", c.ID, err)
		return
	}

	if newest.IsZero() {
		// We didn't find any successful charges, so the first one must
		// have failed.  That means the customer was told their monthly
		// donation request failed, so we shouldn't charge them.  Mark
		// it done.
		if _, err := customer.Update(c.ID, &stripe.CustomerParams{Params: stripe.Params{Metadata: map[string]string{
			"monthly-donation-count": "",
		}}}); err != nil {
			belog.Log("mark failed customer %s done: %s", c.ID, err)
		}
		if verbose {
			fmt.Println(belog.LogMode, "    No successful charges")
		}
		return
	}

	if c.Metadata["monthly-donation-count"] != "-1" {
		// There's a limit on the number of charges the customer wanted,
		// so we should check whether we've reached that limit.
		wanted, err := strconv.Atoi(c.Metadata["monthly-donation-count"])
		if err != nil || wanted < 1 {
			belog.Log("bad count for %s: %s", c.ID, err)
			return
		}
		if count > wanted {
			belog.Log("too many charges for %s", c.ID)
			return
		}
		if count == wanted {
			// This customer is already done.  We should have marked
			// that before, but maybe it failed.
			if _, err := customer.Update(c.ID, &stripe.CustomerParams{Params: stripe.Params{Metadata: map[string]string{
				"monthly-donation-count": "",
			}}}); err != nil {
				belog.Log("mark customer %s already done: %s", c.ID, err)
			}
			if verbose {
				fmt.Println(belog.LogMode, "    Already reached payment limit")
			}
			return
		}
		last = count == wanted-1
	}

	// Find out whether the next payment is due.
	day := newest.Day()
	if day > 28 {
		day = 28
	}
	due := time.Date(newest.Year(), newest.Month()+1, day, 0, 0, 0, 0, time.Local)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	if due.After(today) {
		if verbose {
			fmt.Println(belog.LogMode, "    Next payment not due yet")
		}
		return
	}

	// Get an order number.
	ordernum := getOrderNumber()
	if ordernum == 0 {
		return
	}

	// Create an order.
	var o *stripe.Order
	var amount int64
	var err error
	if amount, err = strconv.ParseInt(c.Metadata["monthly-donation-amount"], 10, 64); err != nil || amount < 1 {
		belog.Log("bad amount for %s: %s", c.ID, err)
	}
	oparams := &stripe.OrderParams{
		Currency: stripe.String(string(stripe.CurrencyUSD)),
		Customer: &c.ID,
		Items: []*stripe.OrderItemParams{{
			Amount:      stripe.Int64(amount * 100),
			Currency:    stripe.String(string(stripe.CurrencyUSD)),
			Description: stripe.String("Tax-Deductible Donation"),
			Parent:      stripe.String("donation"),
			Quantity:    &amount,
			Type:        stripe.String(string(stripe.OrderItemTypeSKU)),
		}},
		Params: stripe.Params{
			Metadata: map[string]string{
				"order-number": strconv.Itoa(ordernum),
				"payment-type": "savedCard",
			},
		},
	}
	if o, err = order.New(oparams); err != nil {
		belog.Log("create order %d for %s: %s", ordernum, c.ID, err)
		return
	}

	// Pay the order.
	if o, err = order.Pay(o.ID, &stripe.OrderPayParams{Customer: &c.ID}); err != nil {
		belog.Log("stripe pay order %d: %s", ordernum, err)
		if _, err = order.Update(o.ID, &stripe.OrderUpdateParams{
			Status: stripe.String(string(stripe.OrderStatusCanceled)),
		}); err != nil {
			belog.Log("stripe cancel order %d: %s", ordernum, err)
		}
		return
	}
	if verbose {
		fmt.Println(belog.LogMode, "    Charged successfully")
	}

	// If this was the last payment for this series, mark it done.
	if last {
		if _, err := customer.Update(c.ID, &stripe.CustomerParams{Params: stripe.Params{Metadata: map[string]string{
			"monthly-donation-count": "",
		}}}); err != nil {
			belog.Log("mark customer %s already done: %s", c.ID, err)
		}
		if verbose {
			fmt.Println(belog.LogMode, "    Last payment; marking sequence closed")
		}
	}

	// Send email confirmation of the donation.
	var to []string
	var cmd *exec.Cmd
	var dontext []byte
	var pipe io.WriteCloser

	if dontext, err = ioutil.ReadFile(confirmfile); err != nil {
		belog.Log("%s", err)
		return
	}
	dontext = bytes.Replace(dontext, []byte("DONATION"), []byte(strconv.Itoa(int(amount))), -1)
	to = append(emailTo, c.Email)
	cmd = exec.Command("/home/scholacantorum/bin/send-email", to...)
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
To: %s <%s>
Reply-To: info@scholacantorum.org
Subject: Schola Cantorum Donation #%d

<p>Dear %s,</p>
`, c.Description, c.Email, ordernum, html.EscapeString(c.Description))
	pipe.Write(dontext)
	fmt.Fprintf(pipe, `<p>Sincerely yours,<br>Schola Cantorum</p><p>Web: <a href="https://scholacantorum.org">scholacantorum.org</a><br>Email: <a href="mailto:info@scholacantorum.org">info@scholacantorum.org</a><br>Phone: (650) 254-1700</p>`)
	pipe.Close()

}

func getOrderNumber() int {
	var fh *os.File
	var onum int
	var err error

	if fh, err = os.OpenFile(orderNumberFile, os.O_RDWR, 0); err != nil {
		goto ERROR
	}
	if err = syscall.Flock(int(fh.Fd()), syscall.LOCK_EX); err != nil {
		goto ERROR
	}
	if _, err = fmt.Fscanln(fh, &onum); err != nil {
		goto ERROR
	}
	onum++
	if _, err = fh.Seek(0, os.SEEK_SET); err != nil {
		goto ERROR
	}
	if _, err = fmt.Fprintln(fh, onum); err != nil {
		goto ERROR
	}
	if err = fh.Close(); err != nil {
		goto ERROR
	}
	return onum
ERROR:
	belog.Log("order-number: %s", err)
	return 0
}
