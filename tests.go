// Author: Liam Stanley <me@liamstanley.io>
// Docs: https://marill.liam.sh/
// Repo: https://github.com/Liamraystanley/marill

package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Liamraystanley/marill/scraper"
	"github.com/Liamraystanley/marill/utils"
)

// TODO:
// if 25% of resources fail to load, or over 30 (for sites with 200+ assets), auto fail the result?

const (
	defaultScore = 10.0
	typeGlob     = "glob"
	typeRegex    = "regex"
)

var defaultTestTypes = [...]string{
	"url",           // resource url (https://example.com/test)
	"host",          // resource host (example.com)
	"scheme",        // resource scheme (http/https/etc)
	"body",          // resource html-stripped body
	"html",          // resource html
	"code",          // resource status code (e.g. 200, 500, etc)
	"headers",       // resource headers in string form (tested against each one, being "Header: value")
	"asset_url",     // asset (js/css/img/png) url
	"asset_scheme",  // asset scheme (http/https/etc)
	"asset_code",    // asset status code (e.g. 200, 500, etc)
	"asset_headers", // asset headers in string form
}

// Test represents a type of check, comparing is the resource matches specific inputs
type Test struct {
	Name        string   `json:"name"`      // the name of the test
	Weight      float64  `json:"weight"`    // how much does this test decrease or increase the score
	RawMatch    []string `json:"match"`     // list of glob/regex matches that any can match (OR)
	RawMatchAll []string `json:"match_all"` // list of glob/regex matches that all must match (AND)

	Origin   string       // where the test originated from
	Match    []*TestMatch // the generated list of OR matches
	MatchAll []*TestMatch // the generated list of AND matches
}

// String returns a string implementation of Test
func (t *Test) String() string {
	return fmt.Sprintf("<%s::%s>", t.Name, t.Origin)
}

// TestMatch represents the type of match and query that will be used to match
type TestMatch struct {
	Type    string         // the type of match. e.g. "glob" or "regex"
	Against string         // what to match against (e.g. defaultTestTypes)
	Query   string         // the actual query which we will be using to match with Type
	Regex   *regexp.Regexp // The compiled regex, if the match is regex based
}

func (m *TestMatch) String() string {
	return fmt.Sprintf("<type:%s against:%s query:%s>", m.Type, m.Against, m.Query)
}

// Compare matches data against TestMatch.Query
func (m *TestMatch) Compare(data []string) bool {
	if m.Type == typeGlob {
		for i := 0; i < len(data); i++ {
			if utils.Glob(data[i], m.Query) {
				return true
			}
		}
	} else {
		for i := 0; i < len(data); i++ {
			if m.Regex.MatchString(data[i]) {
				return true
			}
		}
	}

	return false
}

// generateMatches generates computational matches from RawMatch and RawMatchAll
func (t *Test) generateMatches() {
	// start with t.RawMatch (OR)
	for i := 0; i < len(t.RawMatch); i++ {
		match, err := StrToMatch(t, t.RawMatch[i])
		if err != nil {
			out.Fatal(err)
		}

		t.Match = append(t.Match, match)
	}

	// then t.RawMatchAll (AND)
	for i := 0; i < len(t.RawMatchAll); i++ {
		match, err := StrToMatch(t, t.RawMatchAll[i])
		if err != nil {
			out.Fatal(err)
		}

		t.MatchAll = append(t.MatchAll, match)
	}
}

// StrToMatch converts a string based match element into a composed match query
// e.g. from "glob:body:*something*" -> TestMatch
func StrToMatch(test *Test, rawMatch string) (*TestMatch, error) {
	in := strings.SplitN(rawMatch, ":", 3)
	if len(in) != 3 {
		return nil, fmt.Errorf("unable to parse test %s: invalid 'match' containing: %s", test, rawMatch)
	}

	match := &TestMatch{Type: in[0], Against: in[1], Query: in[2]}

	if match.Type != "glob" && match.Type != "regex" {
		return nil, fmt.Errorf("unable to parse test %s: invalid 'match' type: %s", test, match.Type)
	}

	var isin bool
	for i := 0; i < len(defaultTestTypes); i++ {
		if defaultTestTypes[i] == match.Against {
			isin = true
			break
		}
	}
	if !isin {
		return nil, fmt.Errorf("unable to parse test %s: invalid 'match' query: %s (doesn't exist!)", test, match.Against)
	}

	if match.Type == "regex" {
		var err error
		match.Regex, err = regexp.Compile(match.Query)
		if err != nil {
			return nil, fmt.Errorf("test %s has invalid regex (%s): %s", test, match.Query, err)
		}
	}

	return match, nil
}

// parseTests parses a json object or array from a byte array (file, url, etc)
func parseTests(raw []byte, originType, origin string) (tests []*Test, err error) {
	tmp := []*Test{}

	// check to see if it's an array of json tests
	err = json.Unmarshal(raw, &tmp)
	if err != nil {
		t := &Test{}

		// or just a single json test
		err2 := json.Unmarshal(raw, &t)
		if err2 != nil {
			return nil, fmt.Errorf("unable to load asset from %s %s: %s", originType, origin, err)
		}

		tmp = append(tmp, t)
	}

	for i := range tmp {
		tmp[i].Origin = fmt.Sprintf("%s:%s", originType, origin)
		tests = append(tests, tmp[i])
	}

	return tests, nil
}

// genTests compiles a list of tests from various locations
func genTests() (tests []*Test) {
	tmp := []*Test{}

	genTestsFromStd(&tmp)
	genTestsFromPath(&tmp)
	genTestsFromURL(&tmp)

	blacklist := strings.Split(conf.scan.ignoreTest, "|")
	whitelist := strings.Split(conf.scan.matchTest, "|")

	// loop through each test and ensure that they match our criteria, and are safe
	// to start testing against
	for _, test := range tmp {
		var matches bool

		// check to see if it matches our blacklist. if so, ignore it
		if len(conf.scan.ignoreTest) > 0 {
			for _, match := range blacklist {
				if utils.Glob(test.Name, match) {
					matches = true
					break
				}
			}

			if matches {
				continue // skip
			}
		}

		matches = false

		// check to see if it matches our whitelist. if not, ignore it.
		if len(conf.scan.matchTest) > 0 {
			for _, match := range whitelist {
				if !utils.Glob(test.Name, match) {
					matches = true
					break
				}
			}

			if matches {
				continue // skip
			}
		}

		// generate matches
		test.generateMatches()

		tests = append(tests, test)
	}

	// ensure there are no duplicate tests
	names := []string{}
	for i := 0; i < len(tests); i++ {
		for n := 0; n < len(names); n++ {
			if names[n] == tests[i].Name {
				out.Fatalf("duplicate tests found for %s (origin: %s)", tests[i].Name, tests[i].Origin)
			}
		}
		names = append(names, tests[i].Name)
	}

	logger.Printf("loaded a total of %d tests", len(tests))

	return tests
}

// genTestsFromStd reads from builtin tests (e.g. bindata)
func genTestsFromStd(tests *[]*Test) {
	if conf.scan.ignoreStdTests {
		logger.Print("ignoring all standard (built-in) tests per request")
	} else {
		fns := AssetNames()
		logger.Printf("found %d test files", len(fns))
		count := 0
		for i := 0; i < len(fns); i++ {
			file, err := Asset(fns[i])
			if err != nil {
				out.Fatalf("unable to load asset from file %s: %s", fns[i], err)
			}

			parsedTests, err := parseTests(file, "file-builtin", fns[i])
			if err != nil {
				out.Fatal(err)
			}

			*tests = append(*tests, parsedTests...)
			count += len(parsedTests)
		}

		logger.Printf("loaded %d built-in tests", count)
	}
}

// genTestsFromPath reads tests from a user-specified path
func genTestsFromPath(tests *[]*Test) {
	if len(conf.scan.testsFromPath) == 0 {
		return
	}

	var matches []string

	var testPathCheck = func(path string, info os.FileInfo, err error) error {
		if err != nil {
			out.Fatalf("unable to open file '%s' for reading: %s", path, err)
		}

		if info.IsDir() {
			return nil
		}

		if !strings.HasSuffix(path, ".json") {
			return nil
		}

		matches = append(matches, path)

		return nil
	}

	err := filepath.Walk(conf.scan.testsFromPath, testPathCheck)
	if err != nil {
		out.Fatalf("unable to scan path '%s' for tests: %s", conf.scan.testsFromPath, err)
	}

	logger.Printf("found %d test files within path: %s", len(matches), conf.scan.testsFromPath)

	count := 0
	for i := 0; i < len(matches); i++ {
		file, err := ioutil.ReadFile(matches[i])
		if err != nil {
			out.Fatalf("unable to open file '%s' for reading: %s", matches[i], err)
		}

		parsedTests, err := parseTests(file, "file-path", matches[i])
		if err != nil {
			out.Fatalf("unable to parse JSON from file '%s': %s", matches[i], err)
		}

		*tests = append(*tests, parsedTests...)
		count++
	}

	logger.Printf("loaded %d tests from path: %s", count, conf.scan.testsFromPath)
}

// genTestsFromURL reads tests from a user-specified remote http-url
func genTestsFromURL(tests *[]*Test) {
	if len(conf.scan.testsFromURL) == 0 {
		return
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
	}

	logger.Printf("attempting to pull tests from: %s", conf.scan.testsFromURL)

	req, err := http.NewRequest("GET", conf.scan.testsFromURL, nil)
	if err != nil {
		out.Fatalf("unable to load tests from %s: %s", conf.scan.testsFromURL, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		out.Fatalf("in fetch of tests from %s: %s", conf.scan.testsFromURL, err)
	}

	if resp.Body != nil {
		defer resp.Body.Close()
	}

	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		out.Fatalf("unable to parse JSON from %s: %s", conf.scan.testsFromURL, err)
	}

	parsedTests, err := parseTests(bodyBytes, "url", conf.scan.testsFromURL)
	if err != nil {
		out.Fatal(err)
	}

	*tests = append(*tests, parsedTests...)

	logger.Printf("loaded %d tests from url: %s", len(parsedTests), conf.scan.testsFromURL)
}

// checkTests iterates over all domains and runs checks across all domains
func checkTests(results []*scraper.Results, tests []*Test) (completedTests []*TestResult) {
	timer := utils.NewTimer()
	logger.Print("starting test checks")

	for _, dom := range results {
		completedTests = append(completedTests, checkDomain(dom, tests))
	}

	timer.End()
	logger.Printf("finished tests, elapsed time: %ds\n", timer.Result.Seconds)

	for i := 0; i < len(completedTests); i++ {
		if completedTests[i].Domain.Error != nil {
			continue
		}

		if completedTests[i].Score < conf.scan.minScore {
			failedTests := []string{}
			for k := range completedTests[i].MatchedTests {
				failedTests = append(failedTests, k)
			}

			completedTests[i].Domain.Error = errors.New("failed tests: " + strings.Join(failedTests, ", "))
		}
	}

	return completedTests
}

// TestResult represents the result of testing a single resource
type TestResult struct {
	Domain       *scraper.Results   // Origin domain/resource data
	Score        float64            // resulting score, skewed off defaultScore
	MatchedTests map[string]float64 // map of negative affecting tests that were applied
}

// applyScore applies the score from test to the result, assuming test matched
func (res *TestResult) applyScore(test *Test) {
	// TODO: what did it match?

	res.Score += test.Weight

	if _, ok := res.MatchedTests[test.Name]; !ok {
		res.MatchedTests[test.Name] = 0.0
	}
	res.MatchedTests[test.Name] += test.Weight

	logger.Printf("applied test %s score against %s to: %.2f (now %.2f)\n", test, res.Domain.Resource.Response.URL.String(), test.Weight, res.Score)
}

var reHTMLTag = regexp.MustCompile(`<[^>]+>`)

// TestCompare returns what input match type should compare against
func TestCompare(dom *scraper.Results, test *Test, mtype string) (out []string) {
	bodyNoHTML := reHTMLTag.ReplaceAllString(dom.Response.Body, "")

	switch mtype {
	case "url":
		out = append(out, dom.Response.URL.String())
	case "asset_url":
		for i := 0; i < len(dom.Resources); i++ {
			out = append(out, dom.Resources[i].Response.URL.String())
		}
	case "host":
		out = append(out, dom.Response.URL.Host)
	case "asset_host":
		for i := 0; i < len(dom.Resources); i++ {
			out = append(out, dom.Resources[i].Response.URL.Host)
		}
	case "scheme":
		out = append(out, dom.Response.URL.Scheme)
	case "asset_scheme":
		for i := 0; i < len(dom.Resources); i++ {
			out = append(out, dom.Resources[i].Response.URL.Scheme)
		}
	case "path":
		out = append(out, dom.Response.URL.Path)
	case "asset_path":
		for i := 0; i < len(dom.Resources); i++ {
			out = append(out, dom.Resources[i].Response.URL.Path)
		}
	case "body":
		out = append(out, bodyNoHTML)
	case "html":
		out = append(out, dom.Response.Body)
	case "code":
		out = append(out, strconv.Itoa(dom.Response.Code))
	case "asset_code":
		for i := 0; i < len(dom.Resources); i++ {
			out = append(out, strconv.Itoa(dom.Resources[i].Response.Code))
		}
	case "headers":
		for name, values := range dom.Response.Headers {
			hv := fmt.Sprintf("%s: %s", name, strings.Join(values, " "))

			out = append(out, hv)
		}
	case "asset_headers":
		for i := 0; i < len(dom.Resources); i++ {
			for name, values := range dom.Resources[i].Response.Headers {
				hv := fmt.Sprintf("%s: %s", name, strings.Join(values, " "))

				out = append(out, hv)
			}
		}
	}

	return out
}

// TestMatch compares the input test match parameters with the domain
func (res *TestResult) TestMatch(dom *scraper.Results, test *Test) {
	if len(test.Match) > 0 {
		for i := 0; i < len(test.Match); i++ {
			data := TestCompare(dom, test, test.Match[i].Against)

			if test.Match[i].Compare(data) {
				res.applyScore(test)
			}
		}
	}

	if len(test.MatchAll) > 0 {
		for i := 0; i < len(test.MatchAll); i++ {
			data := TestCompare(dom, test, test.MatchAll[i].Against)

			if !test.MatchAll[i].Compare(data) {
				return // skip right to the end, no sense in continuing
			}
		}

		// assume each was matched properly.
		res.applyScore(test)
	}
}

// checkDomain loops through all tests and guages what test score the domain gets
func checkDomain(dom *scraper.Results, tests []*Test) *TestResult {
	res := &TestResult{Domain: dom, Score: defaultScore, MatchedTests: make(map[string]float64)}

	if dom.Error != nil {
		res.Score = 0
		return res
	}

	for _, t := range tests {
		logger.Printf("running test %s against %s", t, dom.Response.URL.String())
		res.TestMatch(dom, t)
	}

	return res
}