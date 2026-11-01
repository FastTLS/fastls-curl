package cli

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

type storedCookie struct {
	cookie   http.Cookie
	hostOnly bool
}
type persistentJar struct {
	jar     *cookiejar.Jar
	mu      sync.Mutex
	records map[string]storedCookie
}

func newPersistentJar() *persistentJar {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &persistentJar{jar: jar, records: make(map[string]storedCookie)}
}
func (j *persistentJar) Cookies(u *url.URL) []*http.Cookie { return j.jar.Cookies(u) }

func cookieHost(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	if ascii, e := idna.Lookup.ToASCII(host); e == nil {
		return ascii
	}
	return host
}
func cookiePath(path string) string {
	if !strings.HasPrefix(path, "/") {
		return "/"
	}
	if i := strings.LastIndex(path, "/"); i > 0 {
		return path[:i]
	}
	return "/"
}
func cookieKey(c http.Cookie) string { return c.Domain + "\n" + c.Path + "\n" + c.Name }

func (j *persistentJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.jar.SetCookies(u, cookies)
	now := time.Now()
	for _, incoming := range cookies {
		if incoming.Valid() != nil {
			continue
		}
		c := *incoming
		if !strings.HasPrefix(c.Path, "/") {
			c.Path = cookiePath(u.Path)
		}
		// Ask the same standard jar to validate the domain/path independently
		// of expiry, so rejected deletion cookies cannot erase other origins.
		probe, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		check := c
		check.MaxAge = 0
		check.Expires = time.Time{}
		probe.SetCookies(u, []*http.Cookie{&check})
		probeURL := *u
		probeURL.Scheme = "https"
		probeURL.Path = c.Path + "/"
		probeURL.RawPath = ""
		accepted := false
		for _, got := range probe.Cookies(&probeURL) {
			if got.Name == c.Name && got.Value == c.Value {
				accepted = true
				break
			}
		}
		if !accepted {
			continue
		}
		host := cookieHost(u.Hostname())
		hostOnly := c.Domain == ""
		if hostOnly {
			c.Domain = host
		} else {
			c.Domain = cookieHost(strings.TrimPrefix(c.Domain, "."))
			suffix, _ := publicsuffix.PublicSuffix(c.Domain)
			if net.ParseIP(c.Domain) != nil || suffix == c.Domain && c.Domain == host {
				hostOnly = true
			}
		}
		if c.MaxAge > 0 {
			c.Expires = now.Add(time.Duration(c.MaxAge) * time.Second)
		}
		key := cookieKey(c)
		if c.MaxAge < 0 || !c.Expires.IsZero() && !c.Expires.After(now) {
			delete(j.records, key)
			continue
		}
		c.MaxAge = 0
		j.records[key] = storedCookie{c, hostOnly}
	}
}

func (j *persistentJar) load(path string, stdin io.Reader) error {
	data, e := inputFile(path, stdin, 4<<20)
	if e != nil {
		return fail(26, "cannot read cookie file: %v", e)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Text()
		httpOnly := strings.HasPrefix(raw, "#HttpOnly_")
		if raw == "" || strings.HasPrefix(raw, "#") && !httpOnly {
			continue
		}
		if httpOnly {
			raw = strings.TrimPrefix(raw, "#HttpOnly_")
		}
		fields := strings.Split(raw, "\t")
		if len(fields) != 7 {
			return fail(26, "invalid Netscape cookie line %d", line)
		}
		if fields[1] != "TRUE" && fields[1] != "FALSE" || fields[3] != "TRUE" && fields[3] != "FALSE" {
			return fail(26, "invalid cookie flags on line %d", line)
		}
		expires, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil || expires < 0 || expires > 253402300799 {
			return fail(26, "invalid cookie expiry on line %d", line)
		}
		domain := cookieHost(strings.TrimPrefix(fields[0], "."))
		if domain == "" || strings.ContainsAny(domain, "/\\\r\n\t :") && net.ParseIP(domain) == nil || !strings.HasPrefix(fields[2], "/") {
			return fail(26, "invalid cookie scope on line %d", line)
		}
		c := &http.Cookie{Name: fields[5], Value: fields[6], Path: fields[2], Secure: fields[3] == "TRUE", HttpOnly: httpOnly}
		if fields[1] == "TRUE" {
			if suffix, _ := publicsuffix.PublicSuffix(domain); suffix == domain || net.ParseIP(domain) != nil {
				return fail(26, "domain cookie scope is not valid on line %d", line)
			}
			c.Domain = domain
		}
		if expires > 0 {
			c.Expires = time.Unix(expires, 0)
		}
		if c.Valid() != nil {
			return fail(26, "invalid cookie value on line %d", line)
		}
		u := &url.URL{Scheme: "https", Host: domain, Path: c.Path}
		if strings.Contains(domain, ":") {
			u.Host = "[" + domain + "]"
		}
		j.SetCookies(u, []*http.Cookie{c})
	}
	if e = scanner.Err(); e != nil {
		return fail(26, "cannot parse cookie file: %v", e)
	}
	return nil
}

func (j *persistentJar) save(path string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	f, e := os.CreateTemp(filepath.Dir(path), ".fcurl-cookies-*")
	if e != nil {
		return fmt.Errorf("cannot create cookie jar: %w", e)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if e = f.Chmod(0600); e != nil {
		return fmt.Errorf("cannot set cookie jar mode: %w", e)
	}
	writer := bufio.NewWriter(f)
	fmt.Fprint(writer, "# Netscape HTTP Cookie File\n# Generated by fcurl\n\n")
	keys := make([]string, 0, len(j.records))
	for key := range j.records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	now := time.Now()
	for _, key := range keys {
		r := j.records[key]
		c := r.cookie
		if !c.Expires.IsZero() && !c.Expires.After(now) {
			continue
		}
		domain := c.Domain
		include := "FALSE"
		if !r.hostOnly {
			domain = "." + domain
			include = "TRUE"
		}
		if c.HttpOnly {
			domain = "#HttpOnly_" + domain
		}
		secure := "FALSE"
		if c.Secure {
			secure = "TRUE"
		}
		expires := int64(0)
		if !c.Expires.IsZero() {
			expires = c.Expires.Unix()
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", domain, include, c.Path, secure, expires, c.Name, c.Value)
	}
	if e = writer.Flush(); e == nil {
		e = f.Sync()
	}
	if closeErr := f.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return fmt.Errorf("cannot write cookie jar: %w", e)
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return fmt.Errorf("cannot replace cookie jar: %w", e)
	}
	return nil
}
