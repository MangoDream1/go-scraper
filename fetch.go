package scraper

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

const MAX_ATTEMPTS = 10

func fetchHref(href string) (io.ReadCloser, error) {
	fmt.Printf("Fetching GET %v\n", href)

	attempt := uint8(0)
	retry := func() (*http.Response, error) {
		if attempt > MAX_ATTEMPTS {
			return nil, errors.New("max attempts reached; cannot continue retrieving url")
		}

		if attempt > 0 {
			backoff := calculateExponentialBackoff(attempt)
			fmt.Printf("Retrying GET %v after %v\n", href, backoff)
			time.Sleep(time.Second * time.Duration(backoff))
		}

		client := &http.Client{}
		req, err := http.NewRequest("GET", href, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Add("User-Agent", "PostmanRuntime/7.29.3")
		req.Header.Add("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
		req.Header.Add("Accept-Language", "en-US,en;q=0.5")
		req.Header.Add("Connection", "keep-alive")

		response, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		return response, nil
	}

	var check func(*http.Response, error) (*http.Response, error)
	check = func(response *http.Response, err error) (*http.Response, error) {
		attempt++

		if err != nil {
			msg := err.Error()

			if stringShouldContainOneFilter(msg, []string{"timeout", "connection reset"}) {
				fmt.Printf("Failed to GET %v; timeout\n", href)
				return check(retry())
			}

			if stringShouldContainOneFilter(msg, []string{"connection refused"}) {
				fmt.Printf("Failed to GET %v; connection refused\n", href)
				return check(retry())
			}

			if stringShouldContainOneFilter(msg, []string{"EOF"}) {
				fmt.Printf("Failed to GET %v; EOF\n", href)
				return check(retry())
			}

			fmt.Printf("Failed to GET %v; unknown error %v\n", href, msg)
			return nil, err
		}

		if response.StatusCode == 503 {
			fmt.Printf("Failed to GET %v; 503 response\n", href)
			return check(retry())
		}

		if response.StatusCode == 404 {
			fmt.Printf("Failed to GET %v; 404 response\n", href)
			return nil, nil
		}

		if response.StatusCode != 200 {
			fmt.Printf("Failed to GET %v; %v response\n", href, response.StatusCode)
			return nil, errors.New("non-200 response; unable to handle request")
		}

		return response, nil
	}

	response, err := check(retry())
	if response == nil {
		return nil, err
	}

	fmt.Printf("Successfully fetched GET %v \n", href)
	return response.Body, nil
}

func calculateExponentialBackoff(a uint8) float64 {
	return math.Pow(2, float64(a))
}

func stringShouldContainOneFilter(s string, filters []string) bool {
	for _, filter := range filters {
		if strings.Contains(s, filter) {
			return true
		}
	}
	return false
}
