package main

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	http.Handle("/backend/publish-site", http.HandlerFunc(handler))
	cgi.Serve(nil)
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Step 1:  Make sure the caller provided a valid password.
	password := r.FormValue("password")
	// Prepare the password for bcrypt.  Raw bcrypt has a 72 character
	// maximum (bad for pass-phrases) and doesn't allow NUL characters (bad
	// for binary).  So we start by hashing and base64-encoding the result.
	// That's what we use as the actual password.
	hashed := sha256.Sum256([]byte(password))
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(hashed)))
	base64.StdEncoding.Encode(encoded, hashed[:])
	found := false
	for _, p := range passwords {
		if bcrypt.CompareHashAndPassword(p, encoded) == nil {
			found = true
			break
		}
	}
	if !found {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Step 2: Build the sandbox site, to make sure that the site builds OK.
	if err := os.Chdir("/home/scholacantorum/schola6p"); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: chdir schola6p: %s\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	cmd := exec.Command("/home/scholacantorum/bin/hugo", "--quiet", "--config", "sandbox.yaml", "--cleanDestinationDir")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: hugo sandbox: %s\n", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Step 3: Build the production site.
	cmd = exec.Command("/home/scholacantorum/bin/hugo", "--quiet", "--config", "production.yaml", "--cleanDestinationDir")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: hugo production: %s\n", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}
