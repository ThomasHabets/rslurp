package main

/*
 Why:
 1) wget -np -np -r creates crap like index*
 2) TCP slow start sucks if server doesn't support keepalive.
*/
import (
	"crypto/tls"
	"crypto/x509"
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
	"runtime"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ThomasHabets/rslurp/pkg/fileout"
)

const (
	version = "0.2beta"
)

var (
	cpuprofile  = flag.String("cpuprofile", "", "write cpu profile to file")
	numWorkers  = flag.Int("workers", 1, "Number of worker threads.")
	dryRun      = flag.Bool("n", false, "Dry run. Don't download anything.")
	matching    = flag.String("matching", "", "Only download files matching this regex.")
	uiTimer     = flag.Duration("ui_delay", time.Second, "Time between progress updates.")
	verbose     = flag.Bool("v", false, "Verbose.")
	out         = flag.String("out", ".", "Output directory.")
	tarOut      = flag.Bool("tar", false, "Write tar file.")
	verifyCert  = flag.Bool("verify_cert", true, "Verify SSL cert of server.")
	fastCiphers = flag.Bool("fast_cipher", false, "Only use fast ciphers (RC4).")
	rootCAFile  = flag.String("root_ca", "", "Root CA.")

	username string
	password string

	fileoutImpl fileout.FileOut
	errorCount  uint32
	rootCAs     *x509.CertPool

	linkRE         = regexp.MustCompile(`href="([^"]+)"`)
	contentRangeRE = regexp.MustCompile(`^bytes (\d+)-(\d+)/(\d+)$`)
)

func init() {
	// TODO: add ability to read these from file or env.
	flag.StringVar(&username, "username", "", "Username to user for basicauth.")
	flag.StringVar(&password, "password", "", "Password to user for basicauth.")
}

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
	if len(username) != 0 {
		req.SetBasicAuth(username, password)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("rslurp %s", version))
	return req, nil
}

// slurp downloads one file to the current directory.
func slurp(client *http.Client, o order, counter *uint64) error {
	if *dryRun {
		return nil
	}
	fn := path.Base(o.url)
	fullFilename := path.Join(*out, fn)

	req, err := newRequest(o.url)
	if err != nil {
		return err
	}

	var wantSize int64
	if fileoutImpl.HasPartial() {
		if info, err := os.Stat(fullFilename); err == nil {
			wantSize = info.Size()
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", wantSize))
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	partial := false
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusPartialContent:
		partial = true
		hs := resp.Header.Get("Content-Range")
		m := contentRangeRE.FindStringSubmatch(hs)
		if len(m) == 0 {
			return fmt.Errorf("partial content with bad Content-Range header %q", hs)
		}
		if got, want := m[1], fmt.Sprint(wantSize); got != want {
			return fmt.Errorf("got partial content with range start %s, want %s", got, want)
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// File fully downloaded.
		// TODO: check that size matches exactly.
		return nil
	default:
		return fmt.Errorf("status not OK for %q: %v", o.url, resp.StatusCode)
	}

	var readTo io.WriteCloser
	var tmpf *os.File
	var fileLength int64

	if fileoutImpl.FixedSizeOnly() && resp.ContentLength < 0 {
		tmpf, err = ioutil.TempFile("", "rslurp-")
		if err != nil {
			return fmt.Errorf("creating tempfile: %v", err)
		}
		os.Remove(tmpf.Name())
		readTo = tmpf
	} else {
		if partial {
			readTo, err = fileoutImpl.Append(fullFilename, resp.ContentLength)
		} else {
			readTo, err = fileoutImpl.Create(fullFilename, resp.ContentLength)
		}
		if err != nil {
			return err
		}
	}
	defer readTo.Close()

	r := &readWrapper{Child: resp.Body, Counter: counter}
	fileLength, err = io.Copy(readTo, r)
	if err != nil {
		return err
	}

	// Wrote to temp file. Rewrite to proper output.
	if fileoutImpl.FixedSizeOnly() && resp.ContentLength < 0 {
		if _, err := tmpf.Seek(0, 0); err != nil {
			return fmt.Errorf("seek(0,0) in tmpfile: %v", err)
		}
		of, err := fileoutImpl.Create(fullFilename, fileLength)
		if err != nil {
			return err
		}
		defer of.Close()
		if _, err := io.Copy(of, tmpf); err != nil {
			return err
		}
	}

	return nil
}

type order struct {
	url string
	ui  chan<- uiMsg
}

func mkClient() *http.Client {
	client := &http.Client{}
	cipherSuites := []uint16{
		tls.TLS_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	}
	if *fastCiphers {
		cipherSuites = []uint16{
			tls.TLS_RSA_WITH_RC4_128_SHA,
			tls.TLS_ECDHE_RSA_WITH_RC4_128_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
		}
	}
	transport := &http.Transport{
		DisableCompression: true,
		TLSClientConfig: &tls.Config{
			CipherSuites: cipherSuites,
		},
	}
	if !*verifyCert {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}
	if rootCAs != nil {
		transport.TLSClientConfig.RootCAs = rootCAs
	}
	client.Transport = transport
	return client
}
func slurper(orders <-chan order, done chan<- struct{}, counter *uint64) {
	client := mkClient()
	defer close(done)
	for order := range orders {
		if *verbose {
			log.Printf("Starting %q", path.Base(order.url))
		}
		if err := slurp(client, order, counter); err != nil {
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

// list takes an URL to a directory and returns a slice of all of them.
// Links absolute and relative links as-is.
func list(url string) ([]string, error) {
	client := mkClient()
	req, err := newRequest(url)
	if err != nil {
		return nil, fmt.Errorf("prepare request dir: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list dir: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP non-200: %d %s", resp.StatusCode, resp.Status)
	}

	s, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

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

// downloadDirs a set of dirs.
//
// It first lists all the dirs, then starts downloads. This is to more
// easily show progress in number of files.
func downloadDirs(url []string) error {
	fileRE, err := regexp.Compile(*matching)
	if err != nil {
		log.Fatalf("Bad matching expression %q: %v", *matching, err)
	}
	var files []string
	for _, d := range url {
		fs, err := list(d)
		if err != nil {
			return err
		}

		if !strings.HasSuffix(d, "/") {
			d += "/"
		}
		for _, fn := range fs {
			if strings.Contains(fn, "/") {
				continue
			}
			if fileRE.MatchString(fn) {
				files = append(files, d+fn)
			}
		}
	}
	downloadFiles(files)
	return nil
}

// downloadFiles downloads all the given files using workers.
func downloadFiles(files []string) {
	var done []chan struct{} // goroutine exit channels.
	for i := 0; i < *numWorkers; i++ {
		done = append(done, make(chan struct{}))
	}

	var counter uint64
	startTime := time.Now()

	uiChan, uiCleanup := uiStart(startTime, len(files))
	defer uiCleanup()

	// Start all workers and enqueue all orders.
	func() {
		orders := make(chan order, len(files))
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

	timer := time.NewTicker(*uiTimer)
	defer timer.Stop()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	for {
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
				cur := atomic.LoadUint64(&counter)
				uiChan <- uiMsg{
					bytes: &cur,
				}
				return
			}
		case <-timer.C:
			cur := atomic.LoadUint64(&counter)
			uiChan <- uiMsg{
				bytes: &cur,
			}
			break
		}
	}
}

func main() {
	flag.Parse()

	if *rootCAFile != "" {
		f, err := os.Open(*rootCAFile)
		if err != nil {
			log.Fatalf("Failed to open %q: %v", *rootCAFile, err)
		}
		defer f.Close()
		d, err := ioutil.ReadAll(f)
		if err != nil {
			log.Fatalf("Failed to read %q: %v", *rootCAFile, err)
		}
		rootCAs = x509.NewCertPool()
		if !rootCAs.AppendCertsFromPEM(d) {
			log.Fatalf("Failed to parse %q", *rootCAFile)
		}
	}
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}
	if *tarOut {
		if *numWorkers != 1 {
			log.Fatalf("Can only use one worker with -tar.")
		}
		taroutf, err := os.Create(*out)
		if err != nil {
			log.Fatalf("Opening output tar file %q: %v", *out, err)
		}
		defer taroutf.Close()
		fileoutImpl = fileout.NewTarOut(taroutf)
	} else {
		fileoutImpl = &fileout.NormalFileOut{}
	}
	defer fileoutImpl.Close()

	runtime.GOMAXPROCS(runtime.NumCPU())

	if flag.NArg() == 0 {
		return
	}

	if err := downloadDirs(flag.Args()); err != nil {
		log.Fatalf("Failed to start download of %q: %v", flag.Args(), err)
	}
	if errorCount > 0 {
		fmt.Printf("Number of errors: %d\n", errorCount)
		os.Exit(1)
	}
}
