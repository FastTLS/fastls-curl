package cli

import (
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
	"time"
)

func TestCookieOrderSurvivesPersistenceAndCurlInterop(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "host-first", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "domain-second", Domain: "example.test", Path: "/"})
			http.Redirect(w, r, "/echo", 302)
			return
		}
		io.WriteString(w, r.Header.Get("Cookie"))
	}))
	defer s.Close()
	file := filepath.Join(t.TempDir(), "jar")
	headers := []string{"-H", "Host: sub.example.test"}
	want := "sid=host-first; sid=domain-second"
	args := append([]string{"-L", "-c", file}, headers...)
	if c, out, diag := invoke(append(args, s.URL+"/set")...); c != 0 || out != want {
		t.Fatalf("initial order: %d %q %s", c, out, diag)
	}
	for i := 0; i < 3; i++ {
		args = append([]string{"-b", file, "-c", file}, headers...)
		if c, out, diag := invoke(append(args, s.URL+"/echo")...); c != 0 || out != want {
			t.Fatalf("reload %d: %d %q %s", i, c, out, diag)
		}
	}
	curl, e := exec.LookPath("curl")
	if e != nil {
		t.Log("reference curl unavailable; native round-trip assertions passed")
		return
	}
	// Read our jar with curl, then read curl's jar with fcurl.
	for _, mode := range []string{"read", "write"} {
		args = []string{"--disable", "--noproxy", "*", "-sS", "--max-time", "5"}
		if mode == "read" {
			args = append(args, "-b", file)
		} else {
			args = append(args, "-L", "-c", file)
		}
		args = append(args, headers...)
		path := "/echo"
		if mode == "write" {
			path = "/set"
		}
		out, e := exec.Command(curl, append(args, s.URL+path)...).Output()
		if e != nil || string(out) != want {
			t.Fatalf("curl %s: %q %v", mode, out, e)
		}
	}
	args = append([]string{"-b", file}, headers...)
	if c, out, diag := invoke(append(args, s.URL+"/echo")...); c != 0 || out != want {
		t.Fatalf("curl jar import: %d %q %s", c, out, diag)
	}
}

func TestCookieOrderUpdateDeletionAndRecreation(t *testing.T) {
	u, _ := url.Parse("https://sub.example.test/")
	j := newPersistentJar()
	host := &http.Cookie{Name: "sid", Value: "host", Path: "/"}
	domain := &http.Cookie{Name: "sid", Value: "domain", Domain: "example.test", Path: "/"}
	j.SetCookies(u, []*http.Cookie{host, domain})
	file := filepath.Join(t.TempDir(), "jar")
	check := func(want string) {
		t.Helper()
		if e := j.save(file); e != nil {
			t.Fatal(e)
		}
		loaded := newPersistentJar()
		if e := loaded.load(file, strings.NewReader("")); e != nil {
			t.Fatal(e)
		}
		for _, jar := range []*persistentJar{j, loaded} {
			var pairs []string
			for _, c := range jar.Cookies(u) {
				pairs = append(pairs, c.Name+"="+c.Value)
			}
			if got := strings.Join(pairs, "; "); got != want {
				t.Fatalf("order %q, want %q", got, want)
			}
		}
	}
	host.Value = "updated"
	j.SetCookies(u, []*http.Cookie{host})
	check("sid=updated; sid=domain")
	host.MaxAge = -1
	j.SetCookies(u, []*http.Cookie{host})
	check("sid=domain")
	host.MaxAge, host.Value = 0, "recreated"
	j.SetCookies(u, []*http.Cookie{host})
	check("sid=domain; sid=recreated")
}

func TestImportedDuplicateCookiesMatchCurl(t *testing.T) {
	curl, e := exec.LookPath("curl")
	if e != nil {
		t.Skip("reference curl unavailable")
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Header.Get("Cookie"))
	}))
	defer s.Close()
	file := filepath.Join(t.TempDir(), "jar")
	for _, text := range []string{
		"127.0.0.1\tFALSE\t/\tFALSE\t0\tsid\tfirst\n127.0.0.1\tFALSE\t/\tFALSE\t0\tsid\tsecond\n",
		"127.0.0.1\tFALSE\t/\tFALSE\t0\tsid\tfirst\n127.0.0.1\tFALSE\t/\tFALSE\t1\tsid\texpired\n",
	} {
		if e := os.WriteFile(file, []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
		out, e := exec.Command(curl, "--disable", "--noproxy", "*", "-sS", "--max-time", "5", "-b", file, s.URL).Output()
		if e != nil {
			t.Fatal(e)
		}
		if c, got, diag := invoke("-b", file, s.URL); c != 0 || got != string(out) {
			t.Fatalf("duplicate import: %d %q %s; curl %q", c, got, diag, out)
		}
	}
}

func TestCookieFileRoundTripAndFailurePersistence(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/set":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "value", Path: "/", HttpOnly: true})
			io.WriteString(w, "set")
		case "/delete":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "gone", Path: "/", MaxAge: -1})
		case "/fail":
			http.SetCookie(w, &http.Cookie{Name: "error-cookie", Value: "saved", Path: "/"})
			w.WriteHeader(404)
		default:
			io.WriteString(w, r.Header.Get("Cookie"))
		}
	}))
	defer s.Close()
	dir := t.TempDir()
	jar := filepath.Join(dir, "cookies.txt")
	if c, _, diag := invoke("-c", jar, s.URL+"/set"); c != 0 {
		t.Fatalf("%d %s", c, diag)
	}
	data, e := os.ReadFile(jar)
	if e != nil || !strings.Contains(string(data), "#HttpOnly_127.0.0.1\tFALSE\t/\tFALSE\t0\tsession\tvalue") {
		t.Fatalf("%s %v", data, e)
	}
	if c, out, diag := invoke("-b", jar, s.URL); c != 0 || out != "session=value" {
		t.Fatalf("%d %q %s", c, out, diag)
	}
	if c, out, diag := invoke("-b", jar, "-b", "explicit=only", s.URL); c != 0 || out != "explicit=only" {
		t.Fatalf("explicit Cookie priority: %d %q %s", c, out, diag)
	}
	if c, _, diag := invoke("-b", jar, "-c", jar, s.URL+"/delete"); c != 0 {
		t.Fatalf("same-file replacement: %d %s", c, diag)
	}
	data, _ = os.ReadFile(jar)
	if strings.Contains(string(data), "session\t") {
		t.Fatalf("deleted cookie persisted: %s", data)
	}
	if c, _, diag := invoke("--fail", "-c", jar, s.URL+"/fail"); c != 22 {
		t.Fatalf("%d %s", c, diag)
	}
	data, _ = os.ReadFile(jar)
	if !strings.Contains(string(data), "error-cookie\tsaved") {
		t.Fatalf("cookies lost on HTTP failure: %s", data)
	}
	if c, _, _ := invoke("-c", filepath.Join(dir, "missing", "jar"), s.URL); c != 23 {
		t.Fatalf("cookie write code %d", c)
	}
	if c, _, _ := invoke("-c", jar, "-o", jar, s.URL); c != 2 {
		t.Fatalf("cookie/body collision code %d", c)
	}
}

func TestCookieScopeSecureExpiryAndPublicSuffix(t *testing.T) {
	jar := newPersistentJar()
	file := filepath.Join(t.TempDir(), "cookies.txt")
	future := time.Now().Add(time.Hour).Unix()
	past := time.Now().Add(-time.Hour).Unix()
	text := fmt.Sprintf("# Netscape HTTP Cookie File\n.example.test\tTRUE\t/\tFALSE\t0\tdomain\tshared\nexample.test\tFALSE\t/\tFALSE\t0\thost\tonly\n#HttpOnly_.example.test\tTRUE\t/private\tTRUE\t%d\tsecure\tprivate\n.example.test\tTRUE\t/\tFALSE\t%d\texpired\told\n", future, past)
	os.WriteFile(file, []byte(text), 0600)
	if e := jar.load(file, strings.NewReader("")); e != nil {
		t.Fatal(e)
	}
	values := func(raw string) string {
		u, _ := url.Parse(raw)
		var pairs []string
		for _, c := range jar.Cookies(u) {
			pairs = append(pairs, c.Name+"="+c.Value)
		}
		return strings.Join(pairs, "; ")
	}
	if got := values("http://sub.example.test/private/x"); got != "domain=shared" {
		t.Fatalf("HTTP subdomain: %q", got)
	}
	if got := values("https://sub.example.test/private/x"); !strings.Contains(got, "secure=private") || strings.Contains(got, "host=only") || strings.Contains(got, "expired") {
		t.Fatalf("HTTPS scope: %q", got)
	}
	if got := values("https://example.test/public"); !strings.Contains(got, "host=only") || strings.Contains(got, "secure=private") {
		t.Fatalf("path/host scope: %q", got)
	}
	u, _ := url.Parse("https://evil.test/")
	jar.SetCookies(u, []*http.Cookie{{Name: "domain", Value: "attack", Domain: "example.test", Path: "/", MaxAge: -1}})
	if got := values("https://sub.example.test/"); got != "domain=shared" {
		t.Fatalf("invalid-domain deletion erased cookie: %q", got)
	}
	if e := jar.save(file); e != nil {
		t.Fatal(e)
	}
	roundtrip := newPersistentJar()
	if e := roundtrip.load(file, strings.NewReader("")); e != nil {
		t.Fatal(e)
	}
	u, _ = url.Parse("https://sub.example.test/private/x")
	if len(roundtrip.Cookies(u)) != 2 {
		t.Fatalf("round-trip cookie count %d", len(roundtrip.Cookies(u)))
	}
	for _, bad := range []string{".com\tTRUE\t/\tFALSE\t0\tsuper\tbad\n", "bad line\n", "example.test\tFALSE\t/\tFALSE\t-1\ta\tb\n", "example.test\tMAYBE\t/\tFALSE\t0\ta\tb\n"} {
		os.WriteFile(file, []byte(bad), 0600)
		if e := newPersistentJar().load(file, strings.NewReader("")); e == nil {
			t.Fatalf("accepted invalid cookie input %q", bad)
		}
	}
}

func TestResponseCookiePublicSuffixAndDefaultPath(t *testing.T) {
	jar := newPersistentJar()
	u, _ := url.Parse("https://login.example.test/account/login")
	jar.SetCookies(u, []*http.Cookie{{Name: "super", Value: "bad", Domain: "test", Path: "/"}, {Name: "ok", Value: "yes"}})
	path := filepath.Join(t.TempDir(), "jar")
	if e := jar.save(path); e != nil {
		t.Fatal(e)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "super") || !strings.Contains(string(data), "login.example.test\tFALSE\t/account\tFALSE\t0\tok\tyes") {
		t.Fatalf("invalid persisted scope: %s", data)
	}
}
