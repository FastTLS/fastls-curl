package cli

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func dataFile(p dataPart) (prefix, path string, file bool) {
	if p.kind == "--data-raw" {
		return "", "", false
	}
	if p.kind == "--data-urlencode" {
		// curl gives name=content precedence even when the name contains '@'.
		if strings.Contains(p.value, "=") {
			return "", "", false
		}
		if index := strings.IndexByte(p.value, '@'); index >= 0 {
			return p.value[:index], p.value[index+1:], true
		}
		return "", "", false
	}
	if strings.HasPrefix(p.value, "@") {
		return "", p.value[1:], true
	}
	return "", "", false
}

func stdinConsumers(o options) int {
	n := 0
	for _, h := range o.headers {
		if h == "@-" {
			n++
		}
	}
	for _, c := range o.cookies {
		if c == "-" {
			n++
		}
	}
	for _, p := range o.data {
		if _, path, file := dataFile(p); file && path == "-" {
			n++
		}
	}
	return n
}

func inputFile(path string, stdin io.Reader, max int64) ([]byte, error) {
	if path != "-" {
		return readLimitedFile(path, max)
	}
	b, e := io.ReadAll(io.LimitReader(stdin, max+1))
	if e == nil && int64(len(b)) > max {
		e = fmt.Errorf("standard input exceeds %d bytes", max)
	}
	return b, e
}

func expandedHeaders(raw []string, stdin io.Reader) ([]string, error) {
	var result []string
	for _, h := range raw {
		if !strings.HasPrefix(h, "@") {
			result = append(result, h)
			continue
		}
		data, e := inputFile(h[1:], stdin, 4<<20)
		if e != nil {
			return nil, fail(26, "cannot read header file: %v", e)
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if line == "" {
				continue
			}
			if len(line) > 64<<10 || len(result) >= 4096 {
				return nil, fail(2, "header file exceeds field limits")
			}
			result = append(result, line)
		}
	}
	return result, nil
}

func requestHeaders(o options, stdin io.Reader) (http.Header, error) {
	raw, e := expandedHeaders(o.headers, stdin)
	if e != nil {
		return nil, e
	}
	h, e := headerEdits(raw)
	if e != nil {
		return nil, fail(2, "invalid request headers")
	}
	setDefault := func(name, value string) error {
		validated, e := headerEdits([]string{name + ":" + value})
		if e != nil {
			return fail(2, "invalid %s option", name)
		}
		if _, exists := h[name]; !exists {
			h[name] = validated[name]
		}
		return nil
	}
	if o.userAgent != nil {
		if e = setDefault("User-Agent", *o.userAgent); e != nil {
			return nil, e
		}
	}
	if o.referer != nil {
		if e = setDefault("Referer", *o.referer); e != nil {
			return nil, e
		}
	}
	if o.user != nil {
		if strings.ContainsAny(*o.user, "\r\n\x00") {
			return nil, fail(2, "invalid Basic credentials")
		}
		if e = setDefault("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(*o.user))); e != nil {
			return nil, e
		}
	}
	if o.bearer != nil {
		if *o.bearer == "" {
			return nil, fail(2, "Bearer token must not be empty")
		}
		if e = setDefault("Authorization", "Bearer "+*o.bearer); e != nil {
			return nil, e
		}
	}
	var inline []string
	for _, c := range o.cookies {
		if strings.Contains(c, "=") {
			inline = append(inline, c)
		}
	}
	if len(inline) > 0 {
		if e = setDefault("Cookie", strings.Join(inline, ";")); e != nil {
			return nil, e
		}
	}
	return h, nil
}

func encodedData(p dataPart, stdin io.Reader) ([]byte, error) {
	prefix, path, file := dataFile(p)
	b := []byte(p.value)
	if file {
		var e error
		b, e = inputFile(path, stdin, maxRequestBytes)
		if e != nil {
			return nil, fail(26, "cannot read request data: %v", e)
		}
	}
	if p.kind == "--data-urlencode" {
		if !file {
			if name, value, hasEquals := strings.Cut(p.value, "="); hasEquals {
				prefix = name
				b = []byte(value)
			}
		}
		// Like curl's data-urlencode, form encoding uses '+' for spaces.
		length := 0
		for _, c := range b {
			if c == ' ' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", rune(c)) {
				length++
			} else {
				length += 3
			}
			if length > maxRequestBytes-len(prefix)-1 {
				return nil, fail(26, "encoded request data exceeds 32 MiB limit")
			}
		}
		encoded := url.QueryEscape(string(b))
		if prefix != "" {
			encoded = prefix + "=" + encoded
		}
		return []byte(encoded), nil
	}
	if file && p.kind == "--data" {
		b = []byte(strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(string(b)))
	}
	return b, nil
}

func appendQuery(target *url.URL, data []byte) error {
	for _, b := range data {
		if b <= 32 || b >= 127 {
			return fail(3, "query data must be URL encoded; use --data-urlencode")
		}
	}
	if len(data) > 0 {
		if target.RawQuery != "" {
			target.RawQuery += "&"
		}
		target.RawQuery += string(data)
	}
	return nil
}
