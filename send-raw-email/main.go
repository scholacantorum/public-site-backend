// send-raw-email sends an email from the admin@scholacantorum.org account.  The
// command line arguments are the destination addresses.  The headers and body
// of the message are given through standard input.
package main

import (
	"bytes"
	"io"
	"net/smtp"
	"os"

	belog "github.com/scholacantorum/public-site-backend/backend-log"
	"github.com/scholacantorum/public-site-backend/private"
)

func main() {
	var buf bytes.Buffer
	var err error

	belog.LogApp = "send-raw-email"
	if _, err = io.Copy(&buf, os.Stdin); err != nil {
		belog.Log("can't read input: %s", err)
		os.Exit(1)
	}
	if err = smtp.SendMail(private.SMTPServer,
		smtp.PlainAuth("", private.SMTPUsername, private.SMTPPassword, private.SMTPHost),
		"admin@scholacantorum.org", os.Args[1:], buf.Bytes()); err != nil {
		belog.Log("can't send email: %s", err)
		os.Exit(1)
	}
}
