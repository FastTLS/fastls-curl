package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func invoke(args ...string) (int, string, string) {
	var out, err bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &out, &err)
	return code, out.String(), err.String()
}
func TestParse(t *testing.T) {
	o, e := parse([]string{"-sSiL", "-XPOST", "--max-time=0.25", "-Hx-test: a", "https://example.com"})
	if e != nil || !o.silent || !o.showError || !o.include || !o.location || o.method != "POST" || o.total != 250*time.Millisecond {
		t.Fatalf("%+v %v", o, e)
	}
	for _, args := range [][]string{
		{"--unknown"}, {"-X"}, {"--max-time", "NaN", "http://a"}, {"--max-time", "Inf", "http://a"},
		{"--max-redirs", "-1", "http://a"}, {"--profile", "a", "--ja3", "b", "http://a"},
		{"http://a", "http://b"}, {"--head", "--json", "{}", "http://a"},
		{"--fail", "--fail-with-body", "http://a"}, {"--ja4r", "q13", "http://a"},
		{"--verbose=yes", "http://a"}, {"--request", "GET\r\nX: x", "http://a"},
	} {
		if _, e := parse(args); e == nil {
			t.Errorf("accepted %q", args)
		}
	}
}
func TestRequestHeadersAndData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.NewEncoder(w).Encode(struct {
			Method  string
			Headers http.Header
			Body    string
		}{r.Method, r.Header, string(b)})
	}))
	defer server.Close()
	code, out, err := invoke("-H", "X-Multi: first", "-H", "X-Multi: second", "-H", "X-Empty;", "-H", "User-Agent:", "--json", `{"x":1}`, server.URL)
	if code != 0 {
		t.Fatalf("%d %s", code, err)
	}
	var got struct {
		Method  string
		Headers http.Header
		Body    string
	}
	if e := json.Unmarshal([]byte(out), &got); e != nil {
		t.Fatal(e)
	}
	if got.Method != "POST" || got.Body != `{"x":1}` || !reflect.DeepEqual(got.Headers.Values("X-Multi"), []string{"first", "second"}) || got.Headers.Get("Content-Type") != "application/json" {
		t.Fatalf("%+v", got)
	}
	if _, ok := got.Headers["User-Agent"]; ok {
		t.Fatalf("suppressed UA sent: %v", got.Headers)
	}
	if values, ok := got.Headers["X-Empty"]; !ok || len(values) != 1 || values[0] != "" {
		t.Fatalf("empty field missing: %v", got.Headers)
	}
	for _, header := range []string{"bad name: x", "X: hello\r\nInjected: yes", "Content-Length: 99", "Host: a", "Host: b"} {
		args := []string{"-H", header, server.URL}
		if header == "Host: b" {
			args = []string{"-H", "Host: a", "-H", header, server.URL}
		}
		if strings.HasPrefix(header, "Host:") && header != "Host: b" {
			continue
		}
		if code, _, _ := invoke(args...); code != 2 {
			t.Errorf("header %q code %d", header, code)
		}
	}
}
func TestDataFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	os.WriteFile(path, []byte("a\r\nb\x00"), 0600)
	for _, tc := range []struct{ kind, want string }{{"--data", "ab"}, {"--data-binary", "a\r\nb\x00"}, {"--json", "a\r\nb\x00"}} {
		got, _, _, err := readData([]dataPart{{tc.kind, "@" + path}}, strings.NewReader(""))
		if err != nil || string(got) != tc.want {
			t.Fatalf("%s %q %v", tc.kind, got, err)
		}
	}
	b, _, _, e := readData([]dataPart{{"--data-binary", "@-"}}, strings.NewReader("from stdin"))
	if e != nil || string(b) != "from stdin" {
		t.Fatalf("%q %v", b, e)
	}
	if _, _, _, e := readData([]dataPart{{"--data", "@missing-file"}}, strings.NewReader("")); errorCode(e) != 26 {
		t.Fatalf("%v", e)
	}
}
func TestRedirects(t *testing.T) {
	var mu sync.Mutex
	var lastMethod, lastBody string
	var targetHeaders http.Header
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		lastMethod = r.Method
		lastBody = string(b)
		targetHeaders = r.Header.Clone()
		mu.Unlock()
		io.WriteString(w, "arrived")
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cookie" {
			if _, e := r.Cookie("session"); e == nil {
				io.WriteString(w, "cookie-ok")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "value", Path: "/"})
			http.Redirect(w, r, "/cookie", 302)
			return
		}
		status := 302
		if r.URL.Path == "/307" {
			status = 307
		}
		http.Redirect(w, r, target.URL, status)
	}))
	defer source.Close()
	if c, out, e := invoke(source.URL); c != 0 || strings.Contains(out, "arrived") {
		t.Fatalf("%d %q %s", c, out, e)
	}
	for _, tc := range []struct{ path, method, body string }{{"/302", "GET", ""}, {"/307", "POST", "abc"}} {
		c, out, e := invoke("-L", "-d", "abc", "-H", "Authorization: secret", "-H", "Cookie: explicit=secret", "-H", "Host: custom.example", source.URL+tc.path)
		if c != 0 || out != "arrived" {
			t.Fatalf("%d %q %s", c, out, e)
		}
		mu.Lock()
		if lastMethod != tc.method || lastBody != tc.body || targetHeaders.Get("Authorization") != "" || targetHeaders.Get("Cookie") != "" {
			t.Errorf("%s %s %v", lastMethod, lastBody, targetHeaders)
		}
		mu.Unlock()
	}
	if c, _, _ := invoke("-L", "--max-redirs", "0", source.URL); c != 47 {
		t.Fatalf("limit: %d", c)
	}
	if c, out, e := invoke("-L", source.URL+"/cookie"); c != 0 || out != "cookie-ok" {
		t.Fatalf("cookie: %d %q %s", c, out, e)
	}
}
func TestTLS(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "secure") }))
	defer s.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600)
	for _, profile := range []string{"", "chrome/156/windows"} {
		args := []string{}
		if profile != "" {
			args = append(args, "--profile", profile)
		}
		if code, _, e := invoke(append(args, s.URL)...); code != 60 {
			t.Fatalf("default verification %s: %d %s", profile, code, e)
		}
		for _, flags := range [][]string{{"-k"}, {"--cacert", ca}} {
			cmd := append(append([]string{}, args...), flags...)
			cmd = append(cmd, s.URL)
			if code, out, e := invoke(cmd...); code != 0 || out != "secure" {
				t.Fatalf("TLS %v: %d %q %s", cmd, code, out, e)
			}
		}
	}
	if code, _, _ := invoke("--cacert", filepath.Join(t.TempDir(), "missing"), s.URL); code != 77 {
		t.Fatalf("CA read code %d", code)
	}
}
func TestHTTP2Profile(t *testing.T) {
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s|%s", r.Proto, r.Header.Get("User-Agent"), strings.Join(r.Header.Values("X-Multi"), ","))
	}))
	s.EnableHTTP2 = true
	s.StartTLS()
	defer s.Close()
	code, out, e := invoke("-k", "--profile", "chrome/156/windows", "-A", "custom-agent", "-H", "X-Multi: a", "-H", "X-Multi: b", s.URL)
	if code != 0 || out != "HTTP/2.0|custom-agent|a,b" {
		t.Fatalf("%d %q %s", code, out, e)
	}
}
func TestOutputFailuresTimeoutAndCompression(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fail":
			w.WriteHeader(404)
			io.WriteString(w, "not-found")
		case "/slow":
			<-r.Context().Done()
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			z := gzip.NewWriter(w)
			io.WriteString(z, "decoded")
			z.Close()
		default:
			w.Header().Add("Set-Cookie", "a=1")
			w.Header().Add("Set-Cookie", "b=2")
			io.WriteString(w, strings.Repeat("x", 100000))
		}
	}))
	defer s.Close()
	for _, tc := range []struct {
		flag string
		code int
		body string
	}{{"--fail", 22, ""}, {"--fail-with-body", 22, "not-found"}} {
		c, out, e := invoke(tc.flag, s.URL+"/fail")
		if c != tc.code || out != tc.body {
			t.Fatalf("%d %q %s", c, out, e)
		}
	}
	if c, out, e := invoke(s.URL + "/fail"); c != 0 || out != "not-found" {
		t.Fatalf("default HTTP error: %d %q %s", c, out, e)
	}
	if c, _, e := invoke("--max-time", "0.05", s.URL+"/slow"); c != 28 {
		t.Fatalf("timeout %d %s", c, e)
	}
	if c, out, e := invoke("--compressed", s.URL+"/gzip"); c != 0 || out != "decoded" {
		t.Fatalf("compression %d %q %s", c, out, e)
	}
	if c, out, e := invoke("-I", s.URL); c != 0 || !strings.HasPrefix(out, "HTTP/1.1 200") || strings.Contains(out, "xxxx") || strings.Count(out, "Set-Cookie:") != 2 {
		t.Fatalf("HEAD: %d %q %s", c, out, e)
	}
	dir := t.TempDir()
	body := filepath.Join(dir, "body")
	headers := filepath.Join(dir, "headers")
	if c, _, e := invoke("-o", body, "-D", headers, s.URL); c != 0 {
		t.Fatalf("file output %d %s", c, e)
	}
	b, _ := os.ReadFile(body)
	if len(b) != 100000 {
		t.Fatalf("output bytes %d", len(b))
	}
	if c, _, _ := invoke("-o", body, "-D", filepath.Join(dir, ".", "body"), s.URL); c != 2 {
		t.Fatalf("same output code %d", c)
	}
	var out bytes.Buffer
	if c := Run(context.Background(), []string{s.URL}, strings.NewReader(""), brokenWriter{}, &out); c != 23 {
		t.Fatalf("write code %d %s", c, out.String())
	}
	if c, _, e := invoke("-s", "--fail", s.URL+"/fail"); c != 22 || e != "" {
		t.Fatalf("silent %d %q", c, e)
	}
	if c, _, e := invoke("-sS", "--fail", s.URL+"/fail"); c != 22 || !strings.Contains(e, "(22)") {
		t.Fatalf("show error %d %q", c, e)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestHTTPProxyCONNECT(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "proxied") }))
	defer origin.Close()
	var wg sync.WaitGroup
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			http.Error(w, "CONNECT required", 400)
			return
		}
		up, e := net.Dial("tcp", r.Host)
		if e != nil {
			http.Error(w, "upstream failed", 502)
			return
		}
		down, buf, e := w.(http.Hijacker).Hijack()
		if e != nil {
			up.Close()
			return
		}
		io.WriteString(down, "HTTP/1.1 200 Connection Established\r\n\r\n")
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer up.Close()
			defer down.Close()
			done := make(chan struct{})
			go func() { io.Copy(up, buf); up.Close(); close(done) }()
			io.Copy(down, up)
			down.Close()
			<-done
		}()
	}))
	defer func() { proxy.Close(); wg.Wait() }()
	if c, out, e := invoke("-k", "-x", proxy.URL, "--profile", "chrome/156/windows", origin.URL); c != 0 || out != "proxied" {
		t.Fatalf("%d %q %s", c, out, e)
	}
}

func TestOfflineAndInformational(t *testing.T) {
	for _, flag := range []string{"--help", "--version", "--list-profiles"} {
		if c, out, e := invoke(flag); c != 0 || len(out) == 0 {
			t.Fatalf("%s: %d %s", flag, c, e)
		}
	}
	if c, _, _ := invoke("--profile", "does/not/exist", "https://example.invalid"); c != 2 {
		t.Fatalf("profile code %d", c)
	}
	for _, tc := range []struct {
		url  string
		code int
	}{{"ftp://example.com", 1}, {"http://user:password@example.com", 3}, {"relative", 3}} {
		if c, _, _ := invoke(tc.url); c != tc.code {
			t.Errorf("%s: %d", tc.url, c)
		}
	}
}

// Enabled by CI's Linux resource job; regular developer tests stay short.
func TestResourceSoak(t *testing.T) {
	if os.Getenv("FCURL_RESOURCE_CHECK") != "1" {
		t.Skip("resource acceptance is opt-in")
	}
	runResourceSoak(t)
}
