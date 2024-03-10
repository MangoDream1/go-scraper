package scraper

import (
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
	AlreadyDownloaded     func(href string) bool
	HasDownloaded         func(href string)
	MaxConcurrentRequests uint
	StartUrl              string
	hrefs                 chan string
	htmls                 chan htmlResponses
	limiter               limiter.Limiter
	output                chan io.ReadCloser
	wg                    *sync.WaitGroup
}

type htmlResponses struct {
	href string
	body io.ReadCloser
}

func (s *Scraper) Start(output chan io.ReadCloser) {
	s.output = output

	s.limiter = limiter.NewLimiter(int8(s.MaxConcurrentRequests))
	s.wg = &sync.WaitGroup{}

	s.hrefs = make(chan string)
	s.htmls = make(chan htmlResponses)

	s.wg.Add(1)
	s.hrefs <- s.StartUrl

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
				err := s.ParseHtml(html.href, html.body)
				if err != nil {
					fmt.Printf("Error occurred parsing %v\n", html.href)
					panic(err)
				}
				s.wg.Done() // complete html addition
			}()

		case href := <-s.hrefs:
			s.wg.Add(1) // add for go-routine for fetching
			go func() {
				defer s.wg.Done() // complete go-routine fetching
				if !s.isValidHref(href) || s.AlreadyDownloaded(href) {
					s.wg.Done() // complete href addition
					return
				}

				s.limiter.Add()
				body, err := fetchHref(href)
				s.limiter.Done()
				s.HasDownloaded(href)

				if err != nil || body == nil {
					fmt.Printf("Error occurred fetching %v; ignoring\n", href)
					s.wg.Done() // complete href addition
					return
				}

				s.wg.Add(1) // add for html addition
				s.htmls <- htmlResponses{href: href, body: body}
				output <- body
				s.wg.Done() // complete href addition
			}()
		}
	}
}

func (s *Scraper) ParseHtml(parentHref string, html io.ReadCloser) error {
	doc, err := goquery.NewDocumentFromReader(html)
	if err != nil {
		return err
	}

	sel := doc.Find("a")
	for i := range sel.Nodes {
		href, exists := sel.Eq(i).Attr("href")

		if !exists {
			continue
		}

		for _, s := range UNALLOWED {
			if strings.Contains(href, s) {
				continue
			}
		}

		cleanedHref := fixMissingHttps(href)
		hostname, err := getHostname(cleanedHref)
		if err != nil {
			return err
		}

		if hostname == "" && cleanedHref[0] != '/' {
			cleanedHref = parentHref + cleanedHref
		}

		s.hrefs <- cleanedHref
		s.wg.Add(1)
	}

	return nil
}

func (s *Scraper) isValidHref(href string) bool {
	if s.AllowedHrefRegex == nil {
		return false // no regex defined, consider all hrefs invalid
	}

	match := s.AllowedHrefRegex.FindStringSubmatch(href)
	if match != nil && len(match[0]) > 0 {
		return true
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

func fixMissingHttps(url string) string {
	if len(url) < 2 {
		return url
	}

	if url[0:2] == "//" {
		return fmt.Sprintf("https://%s", url[2:])
	}

	return url
}
