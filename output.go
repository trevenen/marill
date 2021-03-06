// Author: Liam Stanley <me@liamstanley.io>
// Docs: https://marill.liam.sh/
// Repo: https://github.com/lrstanley/marill

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/lrstanley/marill/utils"
	"github.com/tdewolff/minify"
	"github.com/tdewolff/minify/css"
	"github.com/tdewolff/minify/html"
	"github.com/tdewolff/minify/js"
)

// JSONOutput is the generated json that will be embedded in Angular.
type JSONOutput struct {
	Version     string
	VersionFull string
	GitRevision string
	Out         []*JSONTestResult
	Successful  int
	Failed      int
	Success     bool
	HostFile    string
	TimeScanned string
	ScanConfig  ScanConfig
}

// Bytes returns a bytes array representation of JSONOutput.
func (j *JSONOutput) Bytes() []byte {
	jsonBytes, err := json.Marshal(j)
	if err != nil {
		panic(err)
	}

	return jsonBytes
}

// String returns a string representation of JSONOutput.
func (j *JSONOutput) String() string {
	jsonBytes, err := json.Marshal(j)
	if err != nil {
		panic(err)
	}

	return fmt.Sprintf("%s", jsonBytes)
}

// StringPretty returns a prettified/indented representation of JSONOutput.
func (j *JSONOutput) StringPretty() string {
	jsonBytes, err := json.MarshalIndent(j, "", "    ")
	if err != nil {
		panic(err)
	}

	return fmt.Sprintf("%s", jsonBytes)
}

// JSONTestResult is a wrapper around the test results, providing string
// representations of some errors and other items that during JSON conversion
// get converted to structs.
type JSONTestResult struct {
	*TestResult
	Assets      []*JSONTestResource
	ErrorString string // string representation of any errors
	URLString   string // string representation of the resulting URL.
}

type JSONTestResource struct {
	URL           string
	Code          int
	ContentLength int64
	Error         string
	Time          *utils.TimerResult
	ContentType   string
}

func genJSONOutput(scan *Scan) (*JSONOutput, error) {
	htmlConvertedResults := make([]*JSONTestResult, len(scan.results))
	var hosts string

	for i := 0; i < len(scan.results); i++ {
		htmlConvertedResults[i] = &JSONTestResult{TestResult: scan.results[i]}

		// Make the footprint of assets much smaller.
		if htmlConvertedResults[i].Result.Assets != nil && len(htmlConvertedResults[i].Result.Assets) > 0 {
			for j := 0; j < len(htmlConvertedResults[i].Result.Assets); j++ {
				var errString string
				err := htmlConvertedResults[i].Result.Assets[j].Error
				if err != nil {
					errString = htmlConvertedResults[i].Result.Assets[j].Error.Error()
				}

				htmlConvertedResults[i].Assets = append(htmlConvertedResults[i].Assets, &JSONTestResource{
					URL:           htmlConvertedResults[i].Result.Assets[j].URL,
					Code:          htmlConvertedResults[i].Result.Assets[j].Response.Code,
					ContentLength: htmlConvertedResults[i].Result.Assets[j].Response.ContentLength,
					Error:         errString,
					Time:          htmlConvertedResults[i].Result.Assets[j].Time,
					ContentType:   htmlConvertedResults[i].Result.Assets[j].Response.Headers.Get("Content-Type"),
				})
			}
		}

		if htmlConvertedResults[i].Result.Error != nil {
			htmlConvertedResults[i].ErrorString = htmlConvertedResults[i].Result.Error.Error()
			// make it so errors are still true, but it doesn't bloat the json
			htmlConvertedResults[i].Result.Error = errors.New(htmlConvertedResults[i].ErrorString)
		}

		if htmlConvertedResults[i].Result.Response.URL != nil {
			htmlConvertedResults[i].URLString = htmlConvertedResults[i].Result.Response.URL.String()
		}

		// trim out some of the bulk here
		if len(htmlConvertedResults[i].Result.Response.Body) > 200 {
			htmlConvertedResults[i].Result.Response.Body = htmlConvertedResults[i].Result.Response.Body[0:200] + " [...]"
		}

		if htmlConvertedResults[i].Result.Request.IP != "" {
			hosts += fmt.Sprintf("%s %s\n", htmlConvertedResults[i].Result.Request.IP, htmlConvertedResults[i].Result.Request.URL.Host)
		}
	}

	jsonOut := &JSONOutput{
		VersionFull: getVersion(),
		Version:     version,
		GitRevision: commithash,
		Out:         htmlConvertedResults,
		Successful:  scan.successful,
		Failed:      scan.failed,
		HostFile:    strings.TrimRight(hosts, "\n"),
		Success:     true,
		TimeScanned: time.Now().Format(time.RFC3339),
		ScanConfig:  conf.scan,
	}

	return jsonOut, nil
}

func genHTMLOutput(input *JSONOutput) ([]byte, error) {
	m := minify.New()
	m.AddFunc("text/css", css.Minify)
	m.AddFunc("text/html", html.Minify)
	m.AddFunc("text/javascript", js.Minify)

	// above the necessary static files
	htmlRawTmpl, err := Asset("data/html/index.html")
	if err != nil {
		return nil, err
	}
	jsRawTmpl, err := Asset("data/html/main.js")
	if err != nil {
		return nil, err
	}
	cssRawTmpl, err := Asset("data/html/main.css")
	if err != nil {
		return nil, err
	}

	// minify js and css
	jsTmpl, err := m.String("text/javascript", string(jsRawTmpl))
	if err != nil {
		return nil, err
	}

	cssTmpl, err := m.String("text/css", string(cssRawTmpl))
	if err != nil {
		return nil, err
	}

	jsonStr := fmt.Sprintf("%s", input.String())
	tmpl := template.New("html")
	tmpl.Delims("{[", "]}")
	tmpl = template.Must(tmpl.Parse(string(htmlRawTmpl)))

	var buf bytes.Buffer
	tmpl.Execute(&buf, struct {
		JSON string
		JS   string
		CSS  string
	}{
		JSON: jsonStr,
		JS:   jsTmpl,
		CSS:  cssTmpl,
	})

	htmlBytes, err := m.Bytes("text/html", buf.Bytes())
	if err != nil {
		return nil, err
	}

	return htmlBytes, nil
}
