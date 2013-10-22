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
	"os/signal"
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

func downloadFiles(files []string) {
	var done []chan struct{}
	for i := 0; i < *numWorkers; i++ {
		done = append(done, make(chan struct{}))
	}

	var counter uint64
	startTime := time.Now()

	uiChan, uiCleanup := uiStart(startTime, len(files))
	defer uiCleanup()

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
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, os.Kill)
		for {
			now := time.Now()
			cur := atomic.LoadUint64(&counter)
			if now.Sub(startTime).Seconds() > 1 {
				uiChan <- uiMsg{
					bytes: &cur,
				}
			}
			select {
			case sig := <-c:
				s := "Killed by signal " + sig.String()
				uiChan <- uiMsg{
					msg: &s,
				}
				return
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
