package main

import (
	"fmt"
	"log"
	"os"
	"path"
	"strings"
	"time"
)

type uiMsg struct {
	bytes    *uint64
	msg      *string
	fileDone *string
}

type uiLogger struct {
	uiChan chan<- uiMsg
}

func (l *uiLogger) Write(p []byte) (int, error) {
	s := strings.Trim(string(p), "\n")
	l.uiChan <- uiMsg{
		msg: &s,
	}
	return len(p), nil
}

func ui(startTime time.Time, nf int, c <-chan uiMsg, done chan<- struct{}) {
	defer close(done)
	var fileDoneCount int
	var bytes, lastBytes uint64
	lastTime := startTime
	p := func(m string, nl bool) {
		fmt.Printf("\r%-*s", 80, m)
		if nl {
			fmt.Printf("\n")
		}
	}

	for msg := range c {
		now := time.Now()
		if msg.bytes != nil {
			bytes = *msg.bytes
		}
		if msg.msg != nil {
			p(*msg.msg, true)
		}
		if msg.fileDone != nil {
			if *verbose {
				p(fmt.Sprintf("Done: %q", path.Base(*msg.fileDone)), true)
			}
			fileDoneCount++
		}
		p(fmt.Sprintf("%d / %d files. %d workers. %sB in %d seconds = %sbps. Current: %sbps.",
			fileDoneCount, nf, *numWorkers,
			humanize(float64(bytes), 0),
			int(now.Sub(startTime).Seconds()),
			humanize(float64(bytes)/now.Sub(startTime).Seconds(), 3),
			humanize(float64(bytes-lastBytes)/now.Sub(lastTime).Seconds(), 3),
		), false)
		lastBytes = bytes
		lastTime = now
	}
	fmt.Printf("\n")
}

func uiStart(startTime time.Time, nf int) (chan<- uiMsg, func()) {
	ok := false

	uiDone := make(chan struct{})
	defer func() {
		if !ok {
			close(uiDone)
		}
	}()

	uiChan := make(chan uiMsg, 100)
	defer func() {
		if !ok {
			close(uiChan)
		}
	}()

	log.SetOutput(&uiLogger{
		uiChan: uiChan,
	})
	defer func() {
		if !ok {
			log.SetOutput(os.Stdout)
		}
	}()

	oldFlags := log.Flags()
	log.SetFlags(0)
	defer func() {
		if !ok {
			log.SetFlags(oldFlags)
		}
	}()

	ok = true
	go ui(startTime, nf, uiChan, uiDone)
	return uiChan, func() {
		log.SetOutput(os.Stdout)
		log.SetFlags(oldFlags)
		close(uiChan)
		<-uiDone
	}
}
