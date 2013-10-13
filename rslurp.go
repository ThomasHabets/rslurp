package main

/*
 Why:
 1) wget -np -np -r creates crap like index*
 2) TCP slow start sucks if server doesn't support keepalive.
*/
import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

var (
	numWorkers = flag.Int("workers", 1, "Number of worker threads.")
	dryRun     = flag.Bool("n", false, "Dry run. Don't download anything.")
	matching   = flag.String("matching", "", "Only download files matching this regex.")
	uiTimer    = flag.Duration("ui_delay", time.Second, "Time between progress updates.")
	verbose    = flag.Bool("v", false, "Verbose.")

	errorCount uint32
)

type readWrapper struct {
	Child   io.Reader
	Counter *uint64
}

func (r *readWrapper) Read(p []byte) (int, error) {
	n, err := r.Child.Read(p)
	atomic.AddUint64(r.Counter, uint64(n))
	return n, err
}

type uiMsg struct {
	bytes    *uint64
	msg      *string
	fileDone *string
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

func newRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "rslurp 0.1")
	return req, nil
}

func slurp(client *http.Client, o order, counter *uint64) error {
	if *dryRun {
		return nil
	}
	fn := path.Base(o.url)

	req, err := newRequest(o.url)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status not OK for %q: %v", o.url, resp.StatusCode)
	}

	of, err := os.Create(fn)
	if err != nil {
		return err
	}
	defer of.Close()

	r := &readWrapper{Child: resp.Body, Counter: counter}
	if _, err := io.Copy(of, r); err != nil {
		return err
	}

	return nil
}

type order struct {
	url string
	ui  chan<- uiMsg
}

func slurper(orders <-chan order, done chan<- struct{}, counter *uint64) {
	client := http.Client{}
	defer close(done)
	for order := range orders {
		if *verbose {
			log.Printf("Starting %q", path.Base(order.url))
		}
		if err := slurp(&client, order, counter); err != nil {
			log.Printf("Failed downloading %q: %v", path.Base(order.url), err)
			atomic.AddUint32(&errorCount, 1)
		} else {
			s := order.url
			order.ui <- uiMsg{
				fileDone: &s,
			}
		}
	}
}

func list(url string) ([]string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	s, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	linkRE := regexp.MustCompile("href=\"([^\"]+)\"")

	links := make(map[string]struct{})
	for _, m := range linkRE.FindAllStringSubmatch(string(s), -1) {
		links[m[1]] = struct{}{}
	}
	ret := make([]string, 0, len(links))
	for k := range links {
		ret = append(ret, k)
	}
	return ret, err
}

func humanize(v float64, dec int) string {
	n := 0
	units := []string{
		" ",
		"k",
		"M",
		"G",
		"T",
		"P",
		"E",
	}
	for _ = range units {
		if v < 1000 {
			break
		}
		v /= 1000
		n++
	}
	return fmt.Sprintf("%.*f %s", dec, v, units[n])
}

func downloadDir(url string) error {
	fileRE := regexp.MustCompile(*matching)
	files, err := list(url)
	if err != nil {
		return err
	}
	fs := make([]string, 0, len(files))
	for _, fn := range files {
		if strings.Contains(fn, "/") {
			continue
		}
		if fileRE.MatchString(fn) {
			fs = append(fs, url+fn)
		}
	}
	downloadFiles(fs)
	return nil
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

func downloadFiles(files []string) {
	var done []chan struct{}
	for i := 0; i < *numWorkers; i++ {
		done = append(done, make(chan struct{}))
	}

	var counter uint64
	startTime := time.Now()

	uiDone := make(chan struct{})
	defer func() { <-uiDone }()
	uiChan := make(chan uiMsg, 100)
	log.SetOutput(&uiLogger{uiChan})
	log.SetFlags(0)
	defer func() {
		log.SetOutput(os.Stdout)
		close(uiChan)
	}()
	go ui(startTime, len(files), uiChan, uiDone)

	func() {
		orders := make(chan order, 1000)
		defer close(orders)
		for _, d := range done {
			go slurper(orders, d, &counter)
		}
		for _, fn := range files {
			orders <- order{
				url: fn,
				ui:  uiChan,
			}
		}
	}()
	lastTime := startTime
	var lastCounter uint64
	timer := make(chan struct{})
	go func() {
		for {
			timer <- struct{}{}
			time.Sleep(*uiTimer)
		}
	}()
	func() {
		for {
			now := time.Now()
			cur := atomic.LoadUint64(&counter)
			if now.Sub(startTime).Seconds() > 1 {
				uiChan <- uiMsg{
					bytes: &cur,
				}
			}
			select {
			case <-done[0]:
				done = done[1:]
				if len(done) == 0 {
					return
				}
			case <-timer:
				break
			}
			lastCounter = cur
			lastTime = now
		}
	}()
}

func main() {
	flag.Parse()

	for _, u := range flag.Args() {
		if err := downloadDir(u); err != nil {
			log.Fatalf("Failed to start download of %q: %v", u, err)
		}
	}
	if errorCount > 0 {
		fmt.Printf("Number of errors: %d\n", errorCount)
		os.Exit(1)
	}
}
