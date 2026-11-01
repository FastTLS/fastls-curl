package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSingleRequestCurlDifferential(t *testing.T) {
	curl, e := exec.LookPath("curl")
	if e != nil {
		t.Skip("reference curl unavailable")
	}
	version, _ := exec.Command(curl, "--version").Output()
	t.Logf("reference: %s", strings.SplitN(string(version), "\n", 2)[0])
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/echo", 302)
			return
		}
		body, _ := io.ReadAll(r.Body)
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			http.Error(w, "invalid query", 400)
			return
		}
		// Compare query key/value semantics; escape hex case is URI-equivalent.
		var cookies []string
		for _, c := range r.Cookies() {
			cookies = append(cookies, c.Name+"="+c.Value)
		}
		fmt.Fprintf(w, "%s|%s|%s|%s|%s|%s|%s|%s", r.Method, query.Encode(), body, r.Header.Get("Content-Type"), r.Header.Get("Authorization"), r.Header.Get("Referer"), strings.Join(cookies, ";"), strings.Join(r.Header.Values("X-Test"), ","))
	}))
	defer s.Close()
	dir := t.TempDir()
	data := filepath.Join(dir, "value.txt")
	headers := filepath.Join(dir, "headers.txt")
	referenceHeaders := filepath.Join(dir, "headers-reference.txt")
	os.WriteFile(data, []byte("first\r\nsecond &世界"), 0600)
	os.WriteFile(headers, []byte("X-Test: a\r\nX-Test: b\r\nUser-Agent:\r\n"), 0600)
	os.WriteFile(referenceHeaders, []byte("X-Test: a\nX-Test: b\nUser-Agent:\n"), 0600)
	for _, args := range [][]string{
		{"-G", "--data-urlencode", "q=hello world", "--data-urlencode", "tag=a&b", s.URL + "?existing=1"},
		{"-G", "--data-urlencode", "q=世界", s.URL},
		{"-G", "-X", "POST", "--data-urlencode", "q=a b", s.URL},
		{"--data-urlencode", "=hello &world", s.URL},
		{"--data-urlencode", "name@" + data, s.URL},
		{"--data-urlencode", "@" + data, s.URL},
		{"--data-urlencode", "name=value@literal", s.URL},
		{"--data-urlencode", "k=", "--data-urlencode", "k=next", s.URL},
		{"--data-raw", "@literal", s.URL},
		{"-H", "@" + headers, s.URL},
		{"-u", "user:pass:word", "--basic", s.URL},
		{"--oauth2-bearer", "token", "-H", "Authorization: custom", s.URL},
		{"--oauth2-bearer", "token", s.URL},
		{"-e", "https://ref.example/page?q=1", s.URL},
		{"-e", "https://ref.example", "-H", "Referer: explicit", s.URL},
		{"-b", "a=1", "-b", "b=2", s.URL},
		{"-b", "a=1", "-H", "Cookie: explicit=2", s.URL},
		{"-L", "-G", "--data-urlencode", "q=first", s.URL + "/redirect"},
	} {
		referenceArgs := append([]string{}, args...)
		// Some platform curl builds send stray CR bytes from a CRLF file.
		// Compare the same logical fields through LF; fcurl's two line endings
		// and its exact Cookie formatting are independently asserted below.
		for i, arg := range referenceArgs {
			if arg == "@"+headers {
				referenceArgs[i] = "@" + referenceHeaders
			}
		}
		cmd := exec.Command(curl, append([]string{"--disable", "--silent", "--noproxy", "*"}, referenceArgs...)...)
		var ref bytes.Buffer
		cmd.Stdout = &ref
		if e := cmd.Run(); e != nil {
			t.Fatalf("reference args %q: %v", args, e)
		}
		code, got, diag := invoke(args...)
		if code != 0 || got != ref.String() {
			t.Errorf("%q: fcurl (%d,%q,%s), curl %q", args, code, got, diag, ref.String())
		}
	}
}

func TestInlineCookieFormattingAndHeaderFileLineEndings(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s|%s", r.Header.Get("Cookie"), strings.Join(r.Header.Values("X-Test"), ","), r.Header.Get("User-Agent"))
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "headers")
	for _, newline := range []string{"\n", "\r\n"} {
		os.WriteFile(path, []byte(strings.Join([]string{"X-Test: a", "X-Test: b", "User-Agent:", ""}, newline)), 0600)
		if c, out, diag := invoke("-H", "@"+path, "-b", "a=1", "-b", "b=2", s.URL); c != 0 || out != "a=1;b=2|a,b|" {
			t.Fatalf("line ending %q: %d %q %s", newline, c, out, diag)
		}
	}
}

func TestInputOwnershipValidationAndRedaction(t *testing.T) {
	for _, args := range [][]string{
		{"-H", "@-", "--data-binary", "@-"},
		{"-b", "-", "--data-urlencode", "name@-"},
		{"-H", "@-", "-b", "-"},
		{"--json", "{}", "-G"},
		{"-u", "user"}, {"-u", "u:p", "--oauth2-bearer", "secret"},
		{"--basic", "--oauth2-bearer", "secret"}, {"-c", "-"},
		{"--oauth2-bearer", "top-secret\r\nInjected:yes"},
		{"-e", "top-secret\r\nInjected:yes"}, {"-e", "https://a/;auto"},
	} {
		code, _, diag := invoke(append(args, "http://127.0.0.1:1")...)
		if code != 2 {
			t.Errorf("%q code %d %s", args, code, diag)
		}
		if strings.Contains(diag, "top-secret") {
			t.Errorf("secret appeared in error: %s", diag)
		}
	}
	if _, e := parse([]string{"-H", "@-", "--data-raw", "@-", "http://example.com"}); e != nil {
		t.Fatalf("literal @- incorrectly consumes stdin: %v", e)
	}
	if _, e := inputFile("-", strings.NewReader("12345"), 4); e == nil {
		t.Fatal("stdin size limit was not enforced")
	}
	for _, args := range [][]string{{"-H", "@missing-header-file"}, {"--data-urlencode", "value@missing-data-file"}, {"-b", "missing-cookie-file"}} {
		if code, _, diag := invoke(append(args, "http://127.0.0.1:1")...); code != 26 {
			t.Fatalf("%q code %d %s", args, code, diag)
		}
	}
	if code, _, _ := invoke("-G", "-d", "raw space", "http://127.0.0.1:1"); code != 3 {
		t.Fatalf("invalid query code %d", code)
	}
}

func TestHeaderAndDataStandardInput(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s|%s|%s", r.URL.RawQuery, r.Header.Get("X-Input"), body)
	}))
	defer s.Close()
	for _, tc := range []struct {
		args        []string
		input, want string
	}{
		{[]string{"-H", "@-"}, "X-Input: piped\r\n", "|piped|"},
		{[]string{"-G", "--data-urlencode", "q@-"}, "a b\n", "q=a+b%0A||"},
		{[]string{"-G", "--data-urlencode", "b=2", "--data-urlencode", "a=1", "--data-urlencode", "b=3"}, "", "b=2&a=1&b=3||"},
		{[]string{"--data-raw", "@-"}, "unused", "||@-"},
	} {
		var out, diag bytes.Buffer
		code := Run(context.Background(), append(tc.args, s.URL), strings.NewReader(tc.input), &out, &diag)
		if code != 0 || out.String() != tc.want {
			t.Errorf("%q: %d %q %s", tc.args, code, out.String(), diag.String())
		}
	}
}

func TestNewCredentialsAreStrippedAcrossOrigins(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s", r.Header.Get("Authorization"), r.Header.Get("Cookie"))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	for _, flags := range [][]string{{"-u", "user:pass"}, {"--oauth2-bearer", "token"}, {"-b", "session=secret"}} {
		args := append(append([]string{"-L"}, flags...), source.URL)
		if code, out, diag := invoke(args...); code != 0 || out != "|" {
			t.Fatalf("%q: %d %q %s", flags, code, out, diag)
		}
	}
}
