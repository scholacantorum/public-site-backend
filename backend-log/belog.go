package belog

import (
	"log"
	"os"
)

// LogApp is the name of the back-end application that is doing the logging.  It
// should be set by the app prior to logging anything.
var LogApp = "app?"

// LogMode is the mode the back-end application is running in.  It should be set
// to LIVE or TEST, prior to logging anything.
var LogMode = "mode?"

var logger *log.Logger

// Log emits a log message.
func Log(f string, a ...interface{}) {
	if logger == nil {
		var fh *os.File
		var err error

		if fh, err = os.OpenFile("/home/scholacantorum/backend.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err != nil {
			logger = log.New(os.Stderr, LogApp+": "+LogMode+": ", log.Ldate|log.Ltime)
			logger.Printf("unable to open backend.log, logging to stderr instead: %s", err)
		} else {
			logger = log.New(fh, LogApp+": "+LogMode+": ", log.Ldate|log.Ltime)
		}
	}
	logger.Printf(f, a...)
}
