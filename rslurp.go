package main

/*
 Why: wget -np -np -r creates crap like index*
*/
import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

var (
	numWorkers = flag.Int("workers", 1, "Number of worker threads.")
	dryRun     = flag.Bool("n", false, "Dry run. Don't download anything.")
	matching   = flag.String("matching", "", "Only download files matching this regex.")
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

func slurp(client *http.Client, url string, counter *uint64) error {
	parts := strings.Split(url, "/")
	log.Printf("Downloading %q to %q", url, parts[len(parts)-1])
	if *dryRun {
		return nil
	}
	fn := parts[len(parts)-1]

	req, err := newRequest(url)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status not OK for %q: %v", url, resp.StatusCode)
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

func slurper(orders <-chan string, done chan<- struct{}, counter *uint64) {
	client := http.Client{}
	defer close(done)
	for url := range orders {
		if err := slurp(&client, url, counter); err != nil {
			log.Printf("Failed downloading %q: %v", url, err)
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
	ret := []string{}
	for _, m := range linkRE.FindAllStringSubmatch(string(s), -1) {
		ret = append(ret, m[1])
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
	return fmt.Sprintf("%.*f %s", 3, v, units[n])
}

func downloadDir(url string) {
	fileRE := regexp.MustCompile(*matching)

	files, err := list(url)
	if err != nil {
		log.Fatal(err)
	}

	var done []chan struct{}
	for i := 0; i < *numWorkers; i++ {
		done = append(done, make(chan struct{}))
	}
	var counter uint64
	startTime := time.Now()
	func() {
		c := make(chan string, 1000)
		defer close(c)
		for _, d := range done {
			go slurper(c, d, &counter)
		}
		for _, fn := range files {
			if strings.Contains(fn, "/") {
				continue
			}
			if fileRE.MatchString(fn) {
				c <- url + fn
			}
		}
	}()
	lastTime := startTime
	var lastCounter uint64
	timer := make(chan struct{})
	go func() {
		for {
			timer <- struct{}{}
			time.Sleep(time.Second)
		}
	}()
	func() {
		for {
			now := time.Now()
			cur := atomic.LoadUint64(&counter)
			if now.Sub(startTime).Seconds() > 1 {
				log.Printf("%sB in %d seconds = %sbps. Lately %sbps",
					humanize(float64(cur), 0),
					int(now.Sub(startTime).Seconds()),
					humanize(float64(cur)/now.Sub(startTime).Seconds(), 3),
					humanize(float64(cur-lastCounter)/now.Sub(lastTime).Seconds(), 3),
				)
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
		downloadDir(u)
	}

}
