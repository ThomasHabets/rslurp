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
)

func newRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "rslurp 0.1")
	return req, nil
}

func slurp(client *http.Client, url string) error {
	parts := strings.Split(url, "/")
	log.Printf("Loading %q to %q", url, parts[len(parts)-1])
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

	if _, err := io.Copy(of, resp.Body); err != nil {
		return err
	}

	return nil
}

func slurper(orders <-chan string, done chan<- struct{}) {
	client := http.Client{}
	defer close(done)
	for url := range orders {
		if err := slurp(&client, url); err != nil {
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

func downloadDir(url string) {
	files, err := list(url)
	if err != nil {
		log.Fatal(err)
	}

	done := make(chan struct{})
	func() {
		c := make(chan string)
		defer close(c)
		go slurper(c, done)
		for _, fn := range files {
			if !strings.Contains(fn, "/") {
				c <- url + fn
			}
		}
	}()
	<-done
}

func main() {
	flag.Parse()

	for _, u := range flag.Args() {
		downloadDir(u)
	}

}
