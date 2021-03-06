// Author: Liam Stanley <me@liamstanley.io>
// Docs: https://marill.liam.sh/
// Repo: https://github.com/lrstanley/marill

package scraper

import (
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// getAttr pulls a specific attribute from a token/element
func getAttr(attr string, attrs []html.Attribute) (val string) {
	for _, item := range attrs {
		if item.Key == attr {
			val = item.Val
			break
		}
	}

	return
}

// getSrc crawls the body of the Results page, yielding all img/script/link resources
// so they can later be fetched.
func getSrc(b io.Reader, parent *url.URL) (urls []string) {
	urls = []string{}

	z := html.NewTokenizer(b)

	for {
		// loop through all tokens in the html body response
		tt := z.Next()

		switch {
		case tt == html.ErrorToken:
			// this assumes that there are no further tokens -- end of document

			stripURLDups(&urls)

			return
		case tt == html.StartTagToken || tt == html.SelfClosingTagToken:
			t := z.Token()

			var src string

			switch t.Data {
			case "link":
				src = getAttr("href", t.Attr)

				rel := getAttr("rel", t.Attr)

				if len(rel) > 0 && strings.ToLower(rel) != "stylesheet" && strings.ToLower(rel) != "shortcut icon" {
					continue
				}
			case "script":
				src = getAttr("src", t.Attr)
			case "img":
				src = getAttr("src", t.Attr)
			default:
				continue
			}

			src = fmtTagLinks(src, parent)

			if len(src) == 0 {
				continue
			}

			urls = append(urls, src)
		}
	}
}

var nonPrefixMatch = regexp.MustCompile(`^[a-zA-Z]`)

// fmtTagLinks formats the resulting link from it's previous form -- may it
// be a relative link, absolute link, invalid, etc.
func fmtTagLinks(src string, parent *url.URL) string {
	// this assumes that the resource is something along the lines of:
	//   http://something.com/ -- which we don't care about
	if len(src) == 0 || strings.HasSuffix(src, "/") {
		return ""
	}

	// add trailing slash to the end of the path
	if len(parent.Path) == 0 {
		parent.Path = "/"
	}

	// site was developed using sub-relative paths. E.g:
	// - url: http://domain.com/sub/path and resource: something/main.js
	//   would equal http://domain.com/sub/path/something/main.js
	if !strings.Contains(src, "//") && nonPrefixMatch.MatchString(src) {
		src = fmt.Sprintf("%s://%s/%s", parent.Scheme, parent.Host+strings.TrimRight(parent.Path, "/"), src)
	}

	// TODO: add support for ../../ based urls.

	// site was developed using relative paths. E.g:
	//  - url: http://domain.com/sub/path and resource: ./something/main.js
	//    would equal http://domain.com/sub/path/something/main.js
	if strings.HasPrefix(src, "./") {
		src = fmt.Sprintf("%s://%s/%s", parent.Scheme, parent.Host, strings.SplitN(src, "./", 2)[1])
	}

	// site is loading resources from a remote location that supports both
	// http and https. browsers should natively tack on the current sites
	// protocol to the url. E.g:
	//  - url: http://domain.com/ and resource: //other.com/some-resource.js
	//    generates: http://other.com/some-resource.js
	//  - url: https://domain.com/ and resource: //other.com/some-resource.js
	//    generates: https://other.com/some-resource.js
	if strings.HasPrefix(src, "//") {
		src = parent.Scheme + ":" + src
	}

	// non-host-absolute resource. E.g. resource is loaded based on the docroot
	// of the domain. E.g:
	//  - url: http://domain.com/ and resource: /some-resource.js
	//    generates: http://domain.com/some-resource.js
	//  - url: https://domain.com/sub/resource and resource: /some-resource.js
	//    generates: https://domain.com/some-resource.js
	if strings.HasPrefix(src, "/") {
		src = parent.Scheme + "://" + parent.Host + src
	}

	// ignore anything else that isn't http based. E.g. ftp://, and other svg-like
	// data urls, as we really can't fetch those.
	if parent.Scheme != "http" && parent.Scheme != "https" {
		return ""
	}

	// check to make sure the link is now valid
	if !strings.HasPrefix(strings.ToLower(src), "http") {
		return ""
	}

	return src
}

// stripURLDups strips all duplicate src URLs
func stripURLDups(domains *[]string) {
	var tmp []string

	for _, dom := range *domains {
		isIn := false
		for _, other := range tmp {
			if dom == other {
				isIn = true
				break
			}
		}
		if !isIn {
			tmp = append(tmp, dom)
		}
	}

	*domains = tmp

	return
}
