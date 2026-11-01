// Package cli implements the documented HTTP subset of curl over fastls.
package cli

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type dataPart struct{ kind, value string }
type options struct {
	url, method, output, dump, proxy, ca, proxyCA                 string
	profile, profileFile, ja3, ja4r                               string
	headers                                                       []string
	data                                                          []dataPart
	include, head, location, insecure, silent, showError, verbose bool
	fail, failBody, compressed, help, version, list               bool
	maxRedirs                                                     int
	connect, total                                                time.Duration
}

var valueOptions = map[string]bool{
	"--request": true, "--header": true, "--user-agent": true,
	"--data": true, "--data-binary": true, "--json": true,
	"--output": true, "--dump-header": true, "--proxy": true,
	"--cacert": true, "--proxy-cacert": true, "--profile": true,
	"--profile-file": true, "--ja3": true, "--ja4r": true,
	"--max-redirs": true, "--connect-timeout": true, "--max-time": true,
	"--url": true,
}
var shortOptions = map[byte]string{
	'X': "--request", 'H': "--header", 'A': "--user-agent", 'd': "--data",
	'o': "--output", 'D': "--dump-header", 'x': "--proxy", 'm': "--max-time",
	'i': "--include", 'I': "--head", 'L': "--location", 'k': "--insecure",
	's': "--silent", 'S': "--show-error", 'v': "--verbose", 'f': "--fail",
	'h': "--help", 'V': "--version",
}

func parse(args []string) (options, error) {
	o := options{maxRedirs: 50}
	positional := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !positional && arg == "--" {
			positional = true
			continue
		}
		if positional || !strings.HasPrefix(arg, "-") || arg == "-" {
			if o.url != "" {
				return o, fmt.Errorf("only one URL per invocation is supported")
			}
			o.url = arg
			continue
		}
		var names []string
		var attached string
		hasAttached := false
		if strings.HasPrefix(arg, "--") {
			name, val, ok := strings.Cut(arg, "=")
			names = []string{name}
			attached = val
			hasAttached = ok
		} else {
			for j := 1; j < len(arg); j++ {
				name, ok := shortOptions[arg[j]]
				if !ok {
					return o, fmt.Errorf("unsupported option: -%c", arg[j])
				}
				names = append(names, name)
				if valueOptions[name] {
					if j+1 < len(arg) {
						attached = arg[j+1:]
						hasAttached = true
					}
					break
				}
			}
		}
		for _, name := range names {
			v := ""
			if valueOptions[name] {
				if hasAttached {
					v = attached
				} else {
					i++
					if i >= len(args) {
						return o, fmt.Errorf("%s requires a value", name)
					}
					v = args[i]
				}
			} else if hasAttached {
				return o, fmt.Errorf("%s does not accept a value", name)
			}
			if v == "" {
				switch name {
				case "--request", "--profile", "--profile-file", "--ja3", "--ja4r", "--cacert", "--proxy-cacert", "--output", "--dump-header", "--url":
					return o, fmt.Errorf("%s requires a nonempty value", name)
				}
			}
			switch name {
			case "--request":
				o.method = v
			case "--header":
				o.headers = append(o.headers, v)
			case "--user-agent":
				o.headers = append(o.headers, "User-Agent: "+v)
				if v == "" {
					o.headers[len(o.headers)-1] = "User-Agent:"
				}
			case "--data", "--data-binary", "--json":
				o.data = append(o.data, dataPart{name, v})
			case "--output":
				o.output = v
			case "--dump-header":
				o.dump = v
			case "--proxy":
				o.proxy = v
			case "--cacert":
				o.ca = v
			case "--proxy-cacert":
				o.proxyCA = v
			case "--profile":
				o.profile = v
			case "--profile-file":
				o.profileFile = v
			case "--ja3":
				o.ja3 = v
			case "--ja4r":
				o.ja4r = v
			case "--url":
				if o.url != "" {
					return o, fmt.Errorf("only one URL per invocation is supported")
				}
				o.url = v
			case "--include":
				o.include = true
			case "--head":
				o.head = true
			case "--location":
				o.location = true
			case "--insecure":
				o.insecure = true
			case "--silent":
				o.silent = true
			case "--show-error":
				o.showError = true
			case "--verbose":
				o.verbose = true
			case "--fail":
				o.fail = true
			case "--fail-with-body":
				o.failBody = true
			case "--compressed":
				o.compressed = true
			case "--help":
				o.help = true
			case "--version":
				o.version = true
			case "--list-profiles":
				o.list = true
			case "--max-redirs":
				n, e := strconv.Atoi(v)
				if e != nil || n < 0 {
					return o, fmt.Errorf("--max-redirs requires a nonnegative integer")
				}
				o.maxRedirs = n
			case "--connect-timeout", "--max-time":
				n, e := strconv.ParseFloat(v, 64)
				if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 86400 {
					return o, fmt.Errorf("%s must be 0..86400 seconds", name)
				}
				d := time.Duration(n * float64(time.Second))
				if name == "--connect-timeout" {
					o.connect = d
				} else {
					o.total = d
				}
			default:
				return o, fmt.Errorf("unsupported option: %s", name)
			}
		}
	}
	count := 0
	for _, s := range []string{o.profile, o.profileFile, o.ja3, o.ja4r} {
		if s != "" {
			count++
		}
	}
	if count > 1 {
		return o, fmt.Errorf("choose only one of --profile, --profile-file, --ja3 and --ja4r")
	}
	if strings.HasPrefix(o.ja4r, "q") {
		return o, fmt.Errorf("HTTP/3 is not supported by this CLI version")
	}
	if o.fail && o.failBody {
		return o, fmt.Errorf("--fail and --fail-with-body are mutually exclusive")
	}
	if o.head && len(o.data) > 0 {
		return o, fmt.Errorf("--head cannot be combined with request data")
	}
	if o.method != "" && !validToken(o.method) {
		return o, fmt.Errorf("invalid request method")
	}
	o.method = strings.ToUpper(o.method)
	jsonMode, otherMode := false, false
	for _, p := range o.data {
		if p.kind == "--json" {
			jsonMode = true
		} else {
			otherMode = true
		}
	}
	if jsonMode && otherMode {
		return o, fmt.Errorf("--json cannot be mixed with other data modes")
	}
	if o.url == "" && !o.help && !o.version && !o.list {
		return o, fmt.Errorf("a URL is required (see --help)")
	}
	if o.output != "" && o.output != "-" && o.output == o.dump {
		return o, fmt.Errorf("body and header output must use different files")
	}
	return o, nil
}

func validToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r > 127 || !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}

// headerEdits preserves repeated field values and distinguishes suppression from
// an explicitly empty field (curl's "Name:" versus "Name;").
func headerEdits(raw []string) (http.Header, error) {
	h := make(http.Header)
	for _, s := range raw {
		name, value, ok := strings.Cut(s, ":")
		if !ok && strings.HasSuffix(s, ";") {
			name = strings.TrimSuffix(s, ";")
			value = ""
			ok = true
			if !validToken(name) {
				return nil, fmt.Errorf("invalid header name")
			}
			h.Add(name, "")
			continue
		}
		if !ok || !validToken(name) || strings.ContainsAny(value, "\r\n\x00") {
			return nil, fmt.Errorf("invalid header: %q", s)
		}
		for _, r := range value {
			if r < 32 && r != '\t' || r == 127 {
				return nil, fmt.Errorf("invalid header value")
			}
		}
		name = http.CanonicalHeaderKey(name)
		value = strings.TrimSpace(value)
		if value == "" {
			h[name] = nil
		} else {
			h.Add(name, value)
		}
	}
	for _, name := range []string{"Content-Length", "Transfer-Encoding", "Trailer", "Proxy-Authorization"} {
		if _, ok := h[name]; ok {
			return nil, fmt.Errorf("%s must be managed by the transport; use request data or proxy URL credentials", name)
		}
	}
	if v, ok := h["Host"]; ok && (len(v) != 1 || v[0] == "") {
		return nil, fmt.Errorf("Host requires exactly one nonempty value")
	}
	return h, nil
}
