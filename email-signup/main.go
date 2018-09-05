// email-signup is a CGI script that signs people up for our email list.  It can
// take email, fname, lname parameters specifying the user's email address and
// name.  Or it can take a single parameter, order, which is a Stripe order ID,
// in which case it will use the email address and name from that order.  In
// either case it talks to MailChimp's API and ensures that the email address is
// either "subscribed" or "pending"; otherwise it puts it into "pending".
package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
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
	defer func() {
		if err := recover(); err != nil {
			belog.Log("PANIC: %s", err)
			panic(err)
		}
	}()
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
	var email, fname, lname string
	oid := strings.TrimSpace(r.FormValue("order"))
	if oid != "" {
		op := stripe.OrderParams{}
		op.AddExpand("customer")
		var o *stripe.Order
		var err error
		if o, err = order.Get(oid, &op); err != nil {
			belog.Log("get order %s: %s", oid, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		email = o.Email
		var names = strings.SplitN(o.Customer.Description, " ", 2)
		fname = names[0]
		if len(names) > 1 {
			lname = names[1]
		}
	} else {
		email = strings.TrimSpace(r.FormValue("email"))
		fname = strings.TrimSpace(r.FormValue("fname"))
		lname = strings.TrimSpace(r.FormValue("lname"))
	}
	if email == "" || fname == "" {
		belog.Log("missing email or name")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Find out from MailChimp whether the user is already subscribed.
	var hash = md5.New()
	hash.Write([]byte(strings.ToLower(email)))
	var emailkey = hex.EncodeToString(hash.Sum(nil))
	var mcbase = fmt.Sprintf("https://%s.api.mailchimp.com/3.0/lists/%s/members/",
		private.MailChimpAPIKey[len(private.MailChimpAPIKey)-3:], private.MailChimpListID)
	var req *http.Request
	var err error
	if req, err = http.NewRequest(http.MethodGet, mcbase+emailkey, nil); err != nil {
		belog.Log("MailChimp NewRequest: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	req.SetBasicAuth("x", private.MailChimpAPIKey)
	var resp *http.Response
	if resp, err = http.DefaultClient.Do(req); err != nil {
		belog.Log("MailChimp get: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var status string
	if resp.StatusCode == http.StatusOK {
		var block struct {
			Status string
		}
		if err = json.NewDecoder(resp.Body).Decode(&block); err == nil && block.Status != "" {
			status = block.Status
		}
	}
	resp.Body.Close()

	// If they're already on the list, there's nothing to do.  Return an
	// appropriate status code.
	if status == "subscribed" {
		belog.Log("%s already subscribed", email)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// If they previously requested a subscription but haven't confirmed,
	// there's nothing we can do.  Return an appropriate status code.
	if status == "pending" {
		belog.Log("%s already pending", email)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// If their status is something else, we want to change it to "pending".
	if status != "" {
		req, err = http.NewRequest(http.MethodPatch, mcbase+emailkey, bytes.NewReader([]byte(`{"status":"pending"}`)))
		if err != nil {
			belog.Log("MailChimp NewRequest: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		req.SetBasicAuth("x", private.MailChimpAPIKey)
		req.Header.Set("Content-Type", "application/json")
		if resp, err = http.DefaultClient.Do(req); err != nil {
			belog.Log("MailChimp patch: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			belog.Log("MailChimp patch: %s", resp.Status)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		belog.Log("%s changed from %s to pending", email, status)
		w.WriteHeader(http.StatusOK)
		return
	}

	// It's a new subscription.
	var body = map[string]interface{}{
		"email_address": email,
		"status":        "pending",
		"email_type":    "html",
		"merge_fields": map[string]string{
			"FNAME": fname,
			"LNAME": lname,
		},
	}
	var bodyenc []byte
	bodyenc, _ = json.Marshal(&body)
	req, err = http.NewRequest(http.MethodPost, mcbase, bytes.NewReader(bodyenc))
	if err != nil {
		belog.Log("MailChimp NewRequest: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	req.SetBasicAuth("x", private.MailChimpAPIKey)
	req.Header.Set("Content-Type", "application/json")
	if resp, err = http.DefaultClient.Do(req); err != nil {
		belog.Log("MailChimp post: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		belog.Log("MailChimp post: %s", resp.Status)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	belog.Log("%s added in pending status", email)
	w.WriteHeader(http.StatusOK)
}
