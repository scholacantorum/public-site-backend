// sku-updated is a CGI script called by a Stripe webhook whenever any SKU or
// product is updated.  It gets the data for all SKUs and products from Stripe
// and renders it in a JSON format in ~/schola6p/data/products.json, where it
// gets used in the site build.  (It does not trigger a site build.)
package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/cgi"
	"os"
	"syscall"

	"github.com/scholacantorum/public-site-backend/backend-log"
	"github.com/scholacantorum/public-site-backend/private"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/product"
	"github.com/stripe/stripe-go/sku"
)

func main() {
	belog.LogApp = "sku-updated"
	http.Handle("/backend/sku-updated", http.HandlerFunc(handler))
	cgi.Serve(nil)
}

func handler(w http.ResponseWriter, r *http.Request) {
	var lockfh *os.File
	var out bytes.Buffer
	var err error

	// Obtain a file lock to ensure that we don't have parallel spreadsheet
	// updates.
	if lockfh, err = os.OpenFile("/home/scholacantorum/order-updated-lock", os.O_CREATE|os.O_RDWR, 0644); err != nil {
		goto ERROR
	}
	defer lockfh.Close()
	if err = syscall.Flock(int(lockfh.Fd()), syscall.LOCK_EX); err != nil {
		goto ERROR
	}

	// First, get the test mode SKUs.
	fmt.Fprint(&out, `{"test":{`)
	stripe.Key = private.StripeTestSecretKey
	stripe.LogLevel = 1
	belog.LogMode = "TEST"
	if err = getSKUs(&out); err != nil {
		goto ERROR
	}

	// Then get the live mode SKUs.
	fmt.Fprint(&out, `},"live":{`)
	stripe.Key = private.StripeLiveSecretKey
	belog.LogMode = "LIVE"
	if err = getSKUs(&out); err != nil {
		goto ERROR
	}

	fmt.Fprintln(&out, "}}")
	if err = ioutil.WriteFile("/home/scholacantorum/schola6p/data/products.json", out.Bytes(), 0644); err != nil {
		goto ERROR
	}
	w.WriteHeader(http.StatusOK)
	return

ERROR:
	belog.Log("%s", err)
	w.WriteHeader(http.StatusInternalServerError)
}

func getSKUs(out *bytes.Buffer) (err error) {
	pi := product.List(&stripe.ProductListParams{Active: stripe.Bool(true)})
	pf := true
	for pi.Next() {
		if pf {
			pf = false
		} else {
			out.WriteByte(',')
		}
		p := pi.Product()
		fmt.Fprintf(out, `%q:{"name":%q,"skus":{`, p.ID, p.Name)
		si := sku.List(&stripe.SKUListParams{Active: stripe.Bool(true), Product: &p.ID})
		sf := true
		for si.Next() {
			if sf {
				sf = false
			} else {
				out.WriteByte(',')
			}
			s := si.SKU()
			fmt.Fprintf(out, `%q:{"price":%d`, s.ID, s.Price)
			for k, v := range s.Attributes {
				if v == "-" {
					// Stripe doesn't allow an attribute to
					// have an empty value, so we fake it
					// with a single hyphen.
					v = ""
				}
				fmt.Fprintf(out, `,%q:%q`, k, v)
			}
			out.WriteByte('}')
		}
		if err = si.Err(); err != nil {
			return err
		}
		out.WriteString("}}")
	}
	return pi.Err()
}
