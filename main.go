package scraper

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/MangoDream1/go-limiter"
	"github.com/PuerkitoBio/goquery"
)

var UNALLOWED [2]string = [2]string{"javascript", "#"}

type Scraper struct {
	AllowedHrefRegex      *regexp.Regexp
	BlockedHrefRegex      *regexp.Regexp
	AlreadyDownloaded     func(href string) bool
	HasDownloaded         func(href string)
	MaxConcurrentRequests int8
	StartUrl              string
	hrefs                 chan string
	htmls                 chan Html
	output                chan Html
	wg                    *sync.WaitGroup
}

type Html struct {
	Href   string
	Body   io.Reader
	output io.Reader
}

func (s *Scraper) Start(output chan Html) {
	s.output = output

	limiter := limiter.NewLimiter(s.MaxConcurrentRequests)
	s.wg = &sync.WaitGroup{}

	s.hrefs = make(chan string)
	s.htmls = make(chan Html)

	s.wg.Add(1)
	go func() {
		s.hrefs <- fixMissingHttps(s.StartUrl)
	}()

	done := make(chan bool)
	go func() {
		s.wg.Wait()
		done <- true
	}()

	for {
		select {
		case <-done:
			fmt.Println("Completed scraping")
			return
		case html := <-s.htmls:
			s.wg.Add(1) // add for go-routine for parsing
			go func() {
				defer s.wg.Done() // complete for go-routine for parsing
				err := s.ParseHtml(html.Href, html.Body)
				if err != nil {
					fmt.Printf("Error occurred parsing %v %v; ignoring\n", html.Href, err)
					return
				}
				s.wg.Done() // complete html addition

				output <- Html{Href: html.Href, Body: html.output}
			}()

		case href := <-s.hrefs:
			s.wg.Add(1) // add for go-routine for fetching
			go func() {
				defer s.wg.Done() // complete go-routine fetching
				if !s.isValidHref(href) || s.AlreadyDownloaded(href) {
					fmt.Printf("Ignoring url: %v failed to pass filter\n", href)
					s.wg.Done() // complete href addition
					return
				}

				limiter.Add()
				body, err := fetchHref(href)
				limiter.Done()
				s.HasDownloaded(href)

				if err != nil || body == nil {
					fmt.Printf("Error occurred fetching %v; ignoring\n", href)
					s.wg.Done() // complete href addition
					return
				}

				bodyW := new(bytes.Buffer)
				bodyT := io.TeeReader(body, bodyW)

				s.wg.Add(1) // add for html addition
				s.htmls <- Html{Href: href, Body: bodyT, output: bodyW}
				s.wg.Done() // complete href addition
			}()
		}
	}
}

func (s *Scraper) ParseHtml(parentHref string, html io.Reader) error {
	doc, err := goquery.NewDocumentFromReader(html)
	if err != nil {
		return err
	}

	sel := doc.Find("a")
	for i := range sel.Nodes {
		href, exists := sel.Eq(i).Attr("href")

		if !exists || href == "" {
			continue
		}

		con := false
		for _, s := range UNALLOWED {
			if strings.Contains(href, s) {
				con = true
				break
			}
		}

		if con {
			continue
		}

		cleanedHref := replaceDoubleSlashWithHttps(href)
		hostname, err := getHostname(cleanedHref)
		if err != nil {
			return err
		}

		if hostname == "" {
			if cleanedHref[0] != '/' {
				cleanedHref, err = url.JoinPath(parentHref, cleanedHref)
				if err != nil {
					return err
				}
			} else {
				parentHostName, err := getHostname(parentHref)
				if err != nil {
					return err
				}

				cleanedHref, err = url.JoinPath(parentHostName, cleanedHref)
				if err != nil {
					return err
				}
			}
		}

		s.hrefs <- fixMissingHttps(cleanedHref)
		s.wg.Add(1)
	}

	return nil
}

func (s *Scraper) isValidHref(href string) bool {
	if s.BlockedHrefRegex != nil {
		match := s.BlockedHrefRegex.FindStringSubmatch(href)
		if len(match) > 0 {
			return false
		}
	}

	if s.AllowedHrefRegex != nil {
		match := s.AllowedHrefRegex.FindStringSubmatch(href)
		if len(match) > 0 {
			return true
		}
	}

	return false
}

func getHostname(rawurl string) (string, error) {
	parsed, err := url.Parse(rawurl)
	if err != nil {
		return "", err
	}
	return parsed.Hostname(), err
}

func replaceDoubleSlashWithHttps(url string) string {
	if len(url) < 2 {
		return url
	}

	if url[0:2] == "//" {
		return fmt.Sprintf("https://%s", url[2:])
	}

	return url
}

func fixMissingHttps(url string) string {
	if !strings.Contains(url, "https://") {
		return fmt.Sprintf("https://%s", url)
	}
	return url
}
