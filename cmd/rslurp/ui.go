package main

import (
	"fmt"
	"log"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh/terminal"
)

const (
	defaultTerminalWidth = 0
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

func humanize(v float64, dec int) string {
	n := 0
	units := []string{
		"",
		"k",
		"M",
		"G",
		"T",
	}
	for _ = range units {
		if v < 1000 {
			break
		}
		v /= 1000
		n++
	}
	if n == 0 {
		dec = 0
	}
	if n >= len(units) {
		return fmt.Sprintf("??? M")
	}
	return fmt.Sprintf("%.*f %s", dec, v, units[n])
}

func roundSeconds(d time.Duration) time.Duration {
	return time.Unix(int64(d.Seconds()), 0).Sub(time.Unix(0, 0))
}

func ui(startTime time.Time, nf int, c <-chan uiMsg, done chan<- struct{}) {
	defer close(done)
	var fileDoneCount int
	var bytes, lastBytes uint64
	lastTime := startTime
	p := func(m string, nl bool) {
		if terminal.IsTerminal(syscall.Stdout) {
			width := defaultTerminalWidth
			if w, _, err := terminal.GetSize(syscall.Stdout); err == nil {
				width = w
			}
			fmt.Printf("\r%-*s\r%s", width-1, "", m)
		} else {
			fmt.Print(m)
		}
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
		elapsed := now.Sub(startTime)
		p(fmt.Sprintf("%d/%d files. %d workers. %sB in %s = %sBps. Current: %sBps",
			fileDoneCount, nf, *numWorkers,
			humanize(float64(bytes), 3),
			roundSeconds(elapsed),
			humanize(float64(bytes)/elapsed.Seconds(), 1),
			humanize(float64(bytes-lastBytes)/now.Sub(lastTime).Seconds(), 1),
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
