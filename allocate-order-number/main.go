package main

import (
	"fmt"
	"net/http"
	"net/http/cgi"
	"os"
	"syscall"

	"github.com/scholacantorum/public-site-backend/backend-log"
	"github.com/scholacantorum/public-site-backend/private"
)

var orderNumberFile string
var orderNumber int

func main() {
	belog.LogApp = "allocate-order-number"
	defer func() {
		if err := recover(); err != nil {
			belog.Log("PANIC: %s", err)
			panic(err)
		}
	}()
	http.Handle("/backend/allocate-order-number", http.HandlerFunc(handler))
	cgi.Serve(nil)
}

func handler(w http.ResponseWriter, r *http.Request) {
	if !checkRequestMethod(w, r) {
		return
	}
	if !checkAuthorization(w, r) {
		return
	}
	if !getMode(w) {
		return
	}
	if !getOrderNumber(w) {
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, orderNumber)
}

func checkRequestMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func checkAuthorization(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != private.CrossSiteKey {
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	return true
}

func getMode(w http.ResponseWriter) bool {
	switch cwd, _ := os.Getwd(); cwd {
	case "/home/scholacantorum/scholacantorum.org/backend", "/home/scholacantorum/scholacantorum.org/public/backend":
		orderNumberFile = "/home/scholacantorum/order-number"
		belog.LogMode = "LIVE"
	case "/home/scholacantorum/new.scholacantorum.org/backend":
		orderNumberFile = "/home/scholacantorum/test-order-number"
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
	if _, err = fmt.Fscanln(fh, &orderNumber); err != nil {
		goto ERROR
	}
	orderNumber++
	if _, err = fh.Seek(0, os.SEEK_SET); err != nil {
		goto ERROR
	}
	if _, err = fmt.Fprintln(fh, orderNumber); err != nil {
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
