package teler

import (
	"bufio"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	"github.com/satyrius/gonx"
	"github.com/valyala/fastjson"
	"ktbs.dev/teler/common"
	"ktbs.dev/teler/pkg/matchers"
)

// Analyze logs from threat resources
func Analyze(options *common.Options, logs *gonx.Entry) (bool, map[string]string) {
	var match bool

	log := make(map[string]string)

	fields := reflect.ValueOf(logs).Elem().FieldByName("fields")
	for _, field := range fields.MapKeys() {
		log[field.String()] = fields.MapIndex(field).String()
	}

	if len(datasets) == 0 {
		getDatasets()
	}

	for cat, data := range datasets {
		log["category"] = cat

		switch cat {
		case "Common Web Attack":
			req, err := url.ParseRequestURI(log["request_uri"])
			if err != nil {
				break
			}

			query := req.Query()
			if len(query) > 0 {
				for p, q := range query {
					dec, err := url.QueryUnescape(strings.Join(q, ""))
					if err != nil {
						continue
					}

					if isWhitelist(options, p+"="+dec) {
						continue
					}

					cwa, _ := fastjson.Parse(data["content"])
					for _, v := range cwa.GetArray("filters") {
						log["category"] = cat + ": " + string(v.GetStringBytes("description"))
						log["element"] = "request_uri"
						quote := regexp.QuoteMeta(dec)

						match = matchers.IsMatch(
							string(v.GetStringBytes("rule")),
							quote,
						)

						if match {
							break
						}
					}
				}
			}
		case "CVE":
			var (
				kind string
				diff *url.URL
				raw  *http.Request
			)

			req, err := url.ParseRequestURI(log["request_uri"])
			if err != nil {
				break
			}

			if isWhitelist(options, req.RequestURI()) {
				break
			}

			log["element"] = "request_uri"
			cves, _ := fastjson.Parse(data["content"])
			for _, cve := range cves.GetArray("templates") {
				log["category"] = strings.ToTitle(string(cve.GetStringBytes("id")))

				for _, r := range cve.GetArray("requests") {
					switch {
					case len(r.GetArray("path")) > 0:
						kind = "path"
					case len(r.GetArray("raw")) > 0:
						kind = "raw"
					}

					if kind == "path" {
						if string(r.GetStringBytes("method")) != log["request_method"] {
							continue
						}
					}

					for _, p := range r.GetArray(kind) {
						switch kind {
						case "path":
							diff, err = url.ParseRequestURI(
								strings.TrimPrefix(
									strings.Trim(p.String(), `"`),
									"{{BaseURL}}",
								),
							)
							if err != nil {
								continue
							}
						case "raw":
							rawURL := strings.Trim(p.String(), `"`)
							rawURL = strings.ReplaceAll(rawURL, "\\n", "\n")
							rawURL = strings.ReplaceAll(rawURL, "\\r", "\r")
							rawURL += "\r\n\r\n"

							raw, err = http.ReadRequest(
								bufio.NewReader(
									strings.NewReader(rawURL),
								),
							)
							if err != nil {
								continue
							}

							if raw.Method != log["request_method"] {
								continue
							}

							diff = raw.URL
						}

						if len(diff.Path) <= 1 {
							continue
						}

						if req.Path != diff.Path {
							break
						}

						fq := 0
						for q := range req.Query() {
							if diff.Query().Get(q) != "" {
								fq++
							}
						}

						if fq >= len(diff.Query()) {
							match = true
							break
						}
					}
				}

				if match {
					break
				}
			}
		case "Bad Crawler":
			log["element"] = "http_user_agent"

			if isWhitelist(options, log["http_user_agent"]) {
				break
			}

			for _, pat := range strings.Split(data["content"], "\n") {
				if match = matchers.IsMatch(pat, log["http_user_agent"]); match {
					break
				}
			}
		case "Bad IP Address":
			log["element"] = "remote_addr"

			if isWhitelist(options, log["remote_addr"]) {
				break
			}

			ips := strings.Split(data["content"], "\n")
			match = matchers.IsMatchFuzz(log["remote_addr"], ips)
		case "Bad Referrer":
			log["element"] = "http_referer"
			if isWhitelist(options, log["http_referer"]) {
				break
			}

			if log["http_referer"] == "-" {
				break
			}

			req, err := url.Parse(log["http_referer"])
			if err != nil {
				break
			}
			refs := strings.Split(data["content"], "\n")

			match = matchers.IsMatchFuzz(req.Host, refs)
		case "Directory Bruteforce":
			log["element"] = "request_uri"

			if isWhitelist(options, log["request_uri"]) ||
				matchers.IsMatch("^20(0|4)$", log["status"]) ||
				matchers.IsMatch("^3[0-9]{2}$", log["status"]) {
				break
			}

			req, err := url.Parse(log["request_uri"])
			if err != nil {
				break
			}

			if req.Path != "/" {
				match = matchers.IsMatch(trimFirst(req.Path), data["content"])
			}
		}

		if match {
			return match, log
		}
	}

	return match, log
}
