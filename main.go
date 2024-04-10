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

var UNALLOWED [3]string = [3]string{"javascript", "#", ".php"}

type scraper struct {
	allowedHrefRegex      *regexp.Regexp
	blockedHrefRegex      *regexp.Regexp
	alreadyDownloaded     func(href string) bool
	hasDownloaded         func(href string)
	maxConcurrentRequests int8
	startUrl              string
	hrefs                 chan string
	htmls                 chan Html
	wg                    *sync.WaitGroup
	startM                *sync.Mutex
}

type Html struct {
	Href   string
	Body   io.Reader
	output io.Reader
}

type Options struct {
	AllowedHrefRegex      *regexp.Regexp
	BlockedHrefRegex      *regexp.Regexp
	AlreadyDownloaded     func(href string) bool
	HasDownloaded         func(href string)
	MaxConcurrentRequests int8
	StartUrl              string
}

func NewScraper(opt Options) *scraper {
	return &scraper{
		allowedHrefRegex:      opt.AllowedHrefRegex,
		blockedHrefRegex:      opt.BlockedHrefRegex,
		alreadyDownloaded:     opt.AlreadyDownloaded,
		hasDownloaded:         opt.HasDownloaded,
		maxConcurrentRequests: opt.MaxConcurrentRequests,
		startUrl:              opt.StartUrl,
		hrefs:                 make(chan string),
		htmls:                 make(chan Html),
		wg:                    &sync.WaitGroup{},
		startM:                &sync.Mutex{},
	}
}

func (s *scraper) Start(output chan Html) {
	limiter := limiter.NewLimiter(s.maxConcurrentRequests)

	s.wg.Add(1)
	go func() {
		s.hrefs <- fixMissingHttps(s.startUrl)
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
				defer s.wg.Done() // complete html addition

				o := make(chan string)
				defer close(o)

				s.wg.Add(1)
				go func() {
					defer s.wg.Done()
					for href := range o {
						s.AddHref(href)
					}
				}()
				err := ParseHtml(html.Href, html.Body, o)

				if err != nil {
					fmt.Printf("Error occurred parsing %v %v; ignoring\n", html.Href, err)
					return
				}

				output <- Html{Href: html.Href, Body: html.output}
			}()

		case href := <-s.hrefs:
			s.wg.Add(1) // add for go-routine for fetching
			go func() {
				defer s.wg.Done() // complete go-routine fetching
				defer s.wg.Done() // complete href addition

				if !s.isValidHref(href) {
					fmt.Printf("Ignoring url: %v failed to pass filter\n", href)
					return
				}

				if s.alreadyDownloaded(href) {
					fmt.Printf("Ignoring url: %v already downloaded\n", href)
					return
				}

				limiter.Add()
				body, err := fetchHref(href)
				limiter.Done()
				s.hasDownloaded(href)

				if err != nil || body == nil {
					fmt.Printf("Error occurred fetching %v; ignoring\n", href)
					return
				}

				bodyW := new(bytes.Buffer)
				bodyT := io.TeeReader(body, bodyW)

				s.wg.Add(1) // add for html addition
				s.htmls <- Html{Href: href, Body: bodyT, output: bodyW}
			}()
		}
	}
}

func (s *scraper) AddHref(href string) {
	s.wg.Add(1)
	s.hrefs <- fixMissingHttps(href)
}

func ParseHtml(parentHref string, html io.Reader, output chan string) error {
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

		output <- fixMissingHttps(cleanedHref)
	}

	return nil
}

func (s *scraper) isValidHref(href string) bool {
	if s.blockedHrefRegex != nil {
		match := s.blockedHrefRegex.FindStringSubmatch(href)
		if len(match) > 0 {
			return false
		}
	}

	if s.allowedHrefRegex != nil {
		match := s.allowedHrefRegex.FindStringSubmatch(href)
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
