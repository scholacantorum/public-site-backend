// This file informs the "mage" command how to build the site.  To use this, run
//
// $ mage pull           to pull the latest files from Github
// $ mage sandbox        to build the sandbox site
// $ mage production     to build the production site
//
// +build mage

package main

import (
	"os"
	"path/filepath"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
	"github.com/magefile/mage/target"
)

func init() {
	os.Chdir(os.Getenv("HOME"))
}

// Pull retrieves the latest bits from all 3 GitHub repos.
func Pull() {
	mg.Deps(PullBackend, PullFramework, PullContent)
}

// PullBackend retrieves the latest bits from the public-site-backend repo.
func PullBackend() error {
	return sh.Run("git", "-C", "schola6p/backend", "pull")
}

// PullFramework retrieves the latest bits from the public-site-framework repo.
func PullFramework() error {
	return sh.Run("git", "-C", "schola6p", "pull")
}

// PullContent retrieves the latest bits from the public-site repo.
func PullContent() error {
	return sh.Run("git", "-C", "schola6p/content", "pull")
}

// Backend builds all of the back-end binaries.
func Backend() {
	mg.Deps(AllocateOrderNumber, EmailSignup, MailSignup, OrderUpdated, PublishSite, SendEmail, SendRawEmail, ToStripe)
}

// AllocateOrderNumber builds and installs the allocate-order-number program.
func AllocateOrderNumber() error {
	if changed, err := target.Dir(
		"schola6p/static/backend/allocate-order-number",
		"schola6p/backend/allocate-order-number", "schola6p/backend/private", "schola6p/backend/backend-log",
	); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.Run("go", "build", "-o", "schola6p/static/backend/allocate-order-number", "./schola6p/backend/allocate-order-number")
}

// EmailSignup builds and installs the email-signup program.
func EmailSignup() error {
	if changed, err := target.Dir(
		"schola6p/static/backend/email-signup",
		"schola6p/backend/email-signup", "schola6p/backend/private", "schola6p/backend/backend-log",
	); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.Run("go", "build", "-o", "schola6p/static/backend/email-signup", "./schola6p/backend/email-signup")
}

// MailSignup builds and installs the mail-signup program.
func MailSignup() error {
	if changed, err := target.Dir(
		"schola6p/static/backend/mail-signup",
		"schola6p/backend/mail-signup", "schola6p/backend/private", "schola6p/backend/backend-log",
	); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.Run("go", "build", "-o", "schola6p/static/backend/mail-signup", "./schola6p/backend/mail-signup")
}

// OrderUpdated builds and installs the order-updated program.
func OrderUpdated() error {
	if changed, err := target.Dir(
		"schola6p/static/backend/order-updated",
		"schola6p/backend/order-updated", "schola6p/backend/private", "schola6p/backend/backend-log",
	); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.Run("go", "build", "-o", "schola6p/static/backend/order-updated", "./schola6p/backend/order-updated")
}

// PublishSite builds and installs the publish-site program.
func PublishSite() error {
	if changed, err := target.Dir(
		"schola6p/static-sandbox/backend/publish-site",
		"schola6p/backend/publish-site", "schola6p/backend/private", "schola6p/backend/backend-log",
	); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.Run("go", "build", "-o", "schola6p/static-sandbox/backend/publish-site", "./schola6p/backend/publish-site")
}

// SendEmail builds and installs the send-email program.
func SendEmail() error {
	if changed, err := target.Dir(
		"bin/send-email", "schola6p/backend/send-email", "schola6p/backend/private", "schola6p/backend/backend-log",
	); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.Run("go", "build", "-o", "bin/send-email", "./schola6p/backend/send-email")
}

// SendEmail builds and installs the send-email program.
func SendEmail() error {
	if changed, err := target.Dir(
		"bin/send-raw-email", "schola6p/backend/send-raw-email", "schola6p/backend/private", "schola6p/backend/backend-log",
	); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.Run("go", "build", "-o", "bin/send-raw-email", "./schola6p/backend/send-raw-email")
}

// ToStripe builds and installs the to-stripe program.
func ToStripe() error {
	if changed, err := target.Dir(
		"schola6p/static/backend/to-stripe",
		"schola6p/backend/to-stripe", "schola6p/backend/private", "schola6p/backend/backend-log",
	); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.Run("go", "build", "-o", "schola6p/static/backend/to-stripe", "./schola6p/backend/to-stripe")
}

func Sandbox() (err error) {
	mg.Deps(Backend)
	if err = os.Chdir("schola6p"); err != nil {
		return err
	}
	defer os.Chdir(os.Getenv("HOME"))
	return sh.Run(filepath.Join(os.Getenv("HOME"), "bin/hugo"), "--config", "sandbox.yaml", "--cleanDestinationDir")
}

func Production() (err error) {
	mg.Deps(Sandbox)
	if err = os.Chdir("schola6p"); err != nil {
		return err
	}
	defer os.Chdir(os.Getenv("HOME"))
	return sh.Run(filepath.Join(os.Getenv("HOME"), "bin/hugo"), "--config", "production.yaml", "--cleanDestinationDir")
}
