package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FastTLS/fastls/profile"
	generators "github.com/FastTLS/imitate"
)

func TestCurlDifferential(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("reference curl is unavailable")
	}
	version, _ := exec.Command(curl, "--version").Output()
	t.Logf("reference: %s", strings.SplitN(string(version), "\n", 2)[0])
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/echo", 302)
			return
		}
		if r.URL.Path == "/error" {
			w.WriteHeader(404)
			io.WriteString(w, "failure")
			return
		}
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s|%s|%s|%s|%s|%s", r.Method, body, strings.Join(r.Header.Values("X-Test"), ","), r.Header.Get("X-Empty"), r.Header.Get("Content-Type"), r.Header.Get("Accept"))
	}))
	defer s.Close()
	for _, args := range [][]string{
		{"-d", "first=1", "-d", "second=2", s.URL},
		{"-d", "", "-d", "second=2", s.URL},
		{"--data-binary", "hello\nworld", "-H", "X-Test: a", "-H", "X-Test: b", "-H", "X-Empty;", s.URL},
		{"--json", `{"ok":true}`, s.URL},
		{"-L", "-d", "x=1", s.URL + "/redirect"},
		{"-L", "-X", "PUT", "-d", "x=1", s.URL + "/redirect"},
		{"-L", "-X", "GET", "-d", "x=1", s.URL + "/redirect"},
		{"-L", "-X", "PUT", "-d", "x=1", "-H", "Content-Type: custom/type", s.URL + "/redirect"},
		{"--fail", s.URL + "/error"}, {"--fail-with-body", s.URL + "/error"},
	} {
		cmdArgs := append([]string{"--disable", "--silent", "--noproxy", "*"}, args...)
		cmd := exec.Command(curl, cmdArgs...)
		var reference bytes.Buffer
		cmd.Stdout = &reference
		refCode := 0
		if e := cmd.Run(); e != nil {
			var ok bool
			var exit *exec.ExitError
			exit, ok = e.(*exec.ExitError)
			if !ok {
				t.Fatal(e)
			}
			refCode = exit.ExitCode()
		}
		gotCode, got, diag := invoke(args...)
		if gotCode != refCode || got != reference.String() {
			t.Errorf("args=%q: fcurl=(%d,%q,%s) curl=(%d,%q)", args, gotCode, got, diag, refCode, reference.String())
		}
	}
}

func TestProfileFileAndCustomFingerprints(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "fingerprint-ok") }))
	defer s.Close()
	r, e := generators.New().Resolve(context.Background(), profile.ResolveRequest{Selector: "chrome/156/windows"})
	if e != nil {
		t.Fatal(e)
	}
	data, e := json.Marshal(r.Spec)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	os.WriteFile(path, data, 0600)
	for _, flags := range [][]string{
		{"--profile-file", path},
		{"--ja3", "771,4865-4866-4867,0-10-13-16-43-45-51,29-23-24,"},
		{"--ja4r", "t13d0307h2_1301,1302,1303_0000,000a,000d,0010,002b,002d,0033_0403,0804,0805,0806"},
	} {
		args := append(append([]string{"-k"}, flags...), s.URL)
		if c, out, diag := invoke(args...); c != 0 || out != "fingerprint-ok" {
			t.Errorf("%v: %d %q %s", flags, c, out, diag)
		}
	}
	for _, text := range []string{string(data) + " {}", `{"unknown_field":true}`, `{`} {
		os.WriteFile(path, []byte(text), 0600)
		if c, _, _ := invoke("--profile-file", path, s.URL); c != 2 {
			t.Errorf("profile error code %d", c)
		}
	}
	for _, flags := range [][]string{{"--ja3", "not-a-ja3"}, {"--ja4r", "not-a-ja4r"}, {"--profile="}, {"--ja3="}} {
		if c, _, _ := invoke(append(flags, s.URL)...); c != 2 {
			t.Errorf("%q code %d", flags, c)
		}
	}
}

func TestCrossOriginKeepsScopedJarCookies(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s|%s", r.Header.Get("Cookie"), r.Header.Get("Authorization"), r.Host)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "jar", Value: "scoped", Path: "/"})
		http.Redirect(w, r, target.URL, 302)
	}))
	defer source.Close()
	c, out, e := invoke("-L", "-H", "Cookie: explicit=secret", "-H", "Authorization: secret", source.URL)
	if c != 0 || out != "jar=scoped||"+strings.TrimPrefix(target.URL, "http://") {
		t.Fatalf("%d %q %s", c, out, e)
	}
	// Go 1.26 scopes jar cookies to an explicit Host. A response for the
	// spoofed virtual host must not become an IP-host cookie after redirect.
	c, out, e = invoke("-L", "-H", "Cookie: explicit=secret", "-H", "Authorization: secret", "-H", "Host: spoofed.example", source.URL)
	if c != 0 || out != "||"+strings.TrimPrefix(target.URL, "http://") {
		t.Fatalf("custom Host cookie isolation: %d %q %s", c, out, e)
	}
}

func TestStreamingDownloadAndCancellation(t *testing.T) {
	const size = 40 << 20
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		block := make([]byte, 64<<10)
		for sent := 0; sent < size; sent += len(block) {
			if _, e := w.Write(block); e != nil {
				return
			}
		}
	}))
	defer s.Close()
	count := &countWriter{}
	if c := Run(context.Background(), []string{s.URL}, strings.NewReader(""), count, io.Discard); c != 0 || count.n != size {
		t.Fatalf("stream code=%d size=%d", c, count.n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	if c := Run(ctx, []string{s.URL + "/wait"}, strings.NewReader(""), io.Discard, io.Discard); c != 130 {
		t.Fatalf("cancel code %d", c)
	}
	if c := Run(context.Background(), []string{"--help"}, strings.NewReader(""), brokenWriter{}, io.Discard); c != 23 {
		t.Fatalf("help output failure code %d", c)
	}
}

type countWriter struct{ n int }

func (w *countWriter) Write(b []byte) (int, error) { w.n += len(b); return len(b), nil }

func TestUserAgentPrecedence(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.Header.Get("User-Agent")) }))
	defer s.Close()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-A", "first", "-A", "second"}, "second"},
		{[]string{"-H", "User-Agent: header", "-A", "agent"}, "header"},
		{[]string{"-A", "agent", "-H", "User-Agent: header"}, "header"},
		{[]string{"--profile", "chrome/156/windows", "-A", "first", "-A", "second"}, "second"},
		{[]string{"-A", ""}, ""},
	} {
		args := append(append([]string{}, tc.args...), s.URL)
		if c, out, e := invoke(args...); c != 0 || out != tc.want {
			t.Errorf("%q: %d %q %s", tc.args, c, out, e)
		}
	}
	if c, _, _ := invoke("-A", "bad\r\nInjected: yes", s.URL); c != 2 {
		t.Fatalf("UA injection code %d", c)
	}
}
