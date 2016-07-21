package main

import "net/http"

func redirectWrap(req *http.Request, via []*http.Request) error {
	// spoof useragent, as there are going to be sites/servers that are
	// setup to deny by a specific useragent string (or lack there of)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/51.0.2704.79 Safari/537.36")

	return nil
}

// Get wraps the standard net/http library, allowing us to spoof hostnames and IP addresses
func Get(url string, ip string) (*http.Response, error) {
	client := &http.Client{
		CheckRedirect: redirectWrap,
	}

	req, err := http.NewRequest("GET", url, nil)

	if err != nil {
		return nil, err
	}

	// if an IP address is provided, rewrite the Host headers
	// of note: if we plan to support custom ports, these should be rewritten
	// within the header. E.g. "hostname.com:8080" -- though, common ports like
	// 80 and 443 are left out.

	// assign the origin host to the host header value
	req.Host = req.URL.Host

	// and overwrite the host used to make the connection
	req.URL.Host = ip

	// actually make the request here
	resp, err := client.Do(req)

	return resp, err
}
