// This file informs the "mage" command how to build and publish the
// back end code.
//
// $ mage [build]   builds the code
// $ mage install   builds the code and install on server
//
//go:build mage
// +build mage

package main

import (
	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
	"github.com/magefile/mage/target"
)

var linux = map[string]string{"GOOS": "linux"}

var Default = Build

// Backend builds all of the back-end binaries.
func Build() {
	mg.Deps(AllocateOrderNumber, EmailSignup, MailSignup, OrderUpdated, PublishSite, ToStripe)
}

// AllocateOrderNumber builds and installs the allocate-order-number program.
func AllocateOrderNumber() error {
	if changed, err := target.Dir("dist/allocate-order-number", "allocate-order-number", "private", "backend-log"); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.RunWith(linux, "go", "build", "-o", "dist/allocate-order-number", "./allocate-order-number")
}

// EmailSignup builds and installs the email-signup program.
func EmailSignup() error {
	if changed, err := target.Dir("dist/email-signup", "email-signup", "private", "backend-log"); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.RunWith(linux, "go", "build", "-o", "dist/email-signup", "./email-signup")
}

// MailSignup builds and installs the mail-signup program.
func MailSignup() error {
	if changed, err := target.Dir("dist/mail-signup", "mail-signup", "private", "backend-log"); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.RunWith(linux, "go", "build", "-o", "dist/mail-signup", "./mail-signup")
}

// OrderUpdated builds and installs the order-updated program.
func OrderUpdated() error {
	if changed, err := target.Dir("dist/order-updated", "order-updated", "private", "backend-log"); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.RunWith(linux, "go", "build", "-o", "dist/order-updated", "./order-updated")
}

// PublishSite builds and installs the publish-site program.
func PublishSite() error {
	if changed, err := target.Dir("dist/publish-site", "publish-site", "private", "backend-log"); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.RunWith(linux, "go", "build", "-o", "dist/publish-site", "./publish-site")
}

// ToStripe builds and installs the to-stripe program.
func ToStripe() error {
	if changed, err := target.Dir("dist/to-stripe", "to-stripe", "private", "backend-log"); err != nil {
		return err
	} else if !changed {
		return nil
	}
	return sh.RunWith(linux, "go", "build", "-o", "dist/to-stripe", "./to-stripe")
}

func Install() (err error) {
	mg.Deps(Build)
	if err = sh.Run("scp", "dist/allocate-order-number", "dist/email-signup", "dist/mail-signup", "dist/order-updated", "dist/to-stripe", "schola:schola6p/static/backend"); err != nil {
		return err
	}
	if err = sh.Run("scp", "dist/publish-site", "schola:schola6p/static-sandbox/backend"); err != nil {
		return err
	}
	if err = sh.Run("ssh", "schola", "bin/hugo --config sandbox.yaml --cleanDestinationDir"); err != nil {
		return err
	}
	return nil
}

func Publish() (err error) {
	return sh.Run("ssh", "schola", "cd schola6p && bin/hugo --config production.yaml --cleanDestinationDir")
}
