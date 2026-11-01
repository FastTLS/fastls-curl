package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls/imitate"
	"github.com/FastTLS/fastls/profile"
	generators "github.com/FastTLS/imitate"
	"golang.org/x/net/publicsuffix"
)

var Version = "0.1.0-dev"

const help = `fcurl - HTTP requests with fastls TLS and HTTP fingerprints
Usage: fcurl [options] <http(s)://URL>

  -X, --request METHOD        Custom HTTP method
  -H, --header 'Name: value'  Repeatable; 'Name:' removes, 'Name;' sends empty
  -A, --user-agent VALUE      Override request User-Agent
  -d, --data DATA             Form data; @file or @- reads and strips CR/LF/NUL
      --data-binary DATA      Binary data; @file or @- reads unchanged
      --json DATA             JSON data and default JSON headers
  -i, --include               Include response headers
  -I, --head                  HEAD request; print headers only
  -o, --output FILE           Body output (default stdout; - means stdout)
  -D, --dump-header FILE      All response headers (- means stdout)
  -L, --location              Follow HTTP(S) redirects (default off)
      --max-redirs N          Maximum redirects (default 50; nonnegative)
  -x, --proxy URL             HTTP/HTTPS/SOCKS5/SOCKS5H proxy, including credentials
      --cacert FILE           Replace origin system roots with PEM CA bundle
      --proxy-cacert FILE     Replace HTTPS proxy system roots with PEM CA bundle
  -k, --insecure              Disable origin certificate verification
      --connect-timeout SEC  Connection phase limit, 0 disables
  -m, --max-time SEC          Total transfer limit including redirects/body
      --compressed           Request gzip/deflate/br/zstd and decode the response
  -f, --fail                  HTTP >=400: exit 22, suppress body
      --fail-with-body       HTTP >=400: exit 22, preserve body
  -s, --silent               Suppress error messages (no progress meter)
  -S, --show-error           Show errors even with --silent
  -v, --verbose              Print method/origin and response status to stderr
      --profile ID           Canonical imitate profile, e.g. chrome/156/windows
      --list-profiles        List available canonical profile IDs
      --profile-file FILE    Load a single strict profile.Spec JSON document
      --ja3 VALUE            Custom JA3 string (not its hash)
      --ja4r VALUE           Custom TCP JA4R string (not its hash)
  -h, --help                 Show this help
  -V, --version              Show fcurl and pinned dependency versions

One URL per invocation. Request data is buffered up to 32 MiB; downloads stream.
No implicit browser profile or environment proxy. HTTP/3 is not supported.
This is a documented curl subset, not a libcurl ABI replacement.
`

type failure struct {
	code int
	err  error
}

func (e *failure) Error() string { return e.err.Error() }
func (e *failure) Unwrap() error { return e.err }
func fail(code int, format string, args ...any) error {
	return &failure{code, fmt.Errorf(format, args...)}
}

// Run keeps I/O injectable and returns a process exit code without exiting.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	stdout = &output{writer: stdout}
	o, err := parse(args)
	if err == nil {
		switch {
		case o.help:
			_, err = io.WriteString(stdout, help)
		case o.version:
			_, err = fmt.Fprintf(stdout, "fcurl %s (fastls 0.2.1; imitate 0.1.2)\n", Version)
		case o.list:
			var ids []string
			ids, err = generators.New().List()
			if err == nil {
				_, err = io.WriteString(stdout, strings.Join(ids, "\n")+"\n")
			}
		default:
			err = execute(ctx, o, stdin, stdout, stderr)
		}
	} else {
		err = &failure{2, err}
	}
	if err == nil {
		return 0
	}
	code := errorCode(err)
	if !o.silent || o.showError {
		fmt.Fprintf(stderr, "fcurl: (%d) %s\n", code, err)
	}
	return code
}

func execute(ctx context.Context, o options, stdin io.Reader, stdout, stderr io.Writer) error {
	target, err := parseURL(o.url)
	if err != nil {
		return err
	}
	h, err := headerEdits(o.headers)
	if err != nil {
		return &failure{2, err}
	}
	body, contentType, isJSON, err := readData(o.data, stdin)
	if err != nil {
		return err
	}
	if contentType != "" {
		if _, ok := h["Content-Type"]; !ok {
			h.Set("Content-Type", contentType)
		}
	}
	if isJSON {
		if _, ok := h["Accept"]; !ok {
			h.Set("Accept", "application/json")
		}
	}
	if o.compressed {
		if _, ok := h["Accept-Encoding"]; !ok {
			h.Set("Accept-Encoding", "gzip, deflate, br, zstd")
		}
	}
	base, err := transportOptions(ctx, o)
	if err != nil {
		return err
	}
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	base.CookieJar = jar
	method := "GET"
	if len(o.data) > 0 {
		method = "POST"
	}
	if o.head {
		method = "HEAD"
	}
	if o.method != "" {
		method = o.method
	}
	if o.total > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.total)
		defer cancel()
	}
	if o.output != "" && o.output != "-" && o.dump != "" && o.dump != "-" {
		a, e1 := filepath.Abs(o.output)
		b, e2 := filepath.Abs(o.dump)
		if e1 == nil && e2 == nil && (a == b || os.PathSeparator == '\\' && strings.EqualFold(a, b) || sameFile(a, b)) {
			return fail(2, "body and header output must use different files")
		}
	}
	out, err := openOutput(o.output, stdout)
	if err != nil {
		return err
	}
	defer out.Close()
	var dump io.WriteCloser
	if o.dump != "" {
		dump, err = openOutput(o.dump, stdout)
		if err != nil {
			return err
		}
		defer dump.Close()
	}
	for hop := 0; ; hop++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		opts := base
		opts.Headers = make(map[string]string)
		for k, v := range base.Headers {
			opts.Headers[k] = v
		}
		opts.HeaderValues = make(map[string][]string)
		for k, v := range base.HeaderValues {
			opts.HeaderValues[k] = append([]string(nil), v...)
		}
		for k, v := range h {
			delete(opts.Headers, k)
			opts.HeaderValues[k] = append([]string(nil), v...)
		}
		// A new fixed-configuration Session per hop prevents suppressed credentials
		// from surviving in a cached request configuration. Cookies share one jar.
		session, e := fastls.NewSession(opts)
		if e != nil {
			return fail(2, "invalid transport configuration: %v", e)
		}
		if o.verbose {
			fmt.Fprintf(stderr, "> %s %s://%s\n", method, target.Scheme, target.Host)
		}
		resp, e := session.DoContext(ctx, target.String(), fastls.RequestOptions{
			Body: string(body), DisableRedirect: true, DecodeContent: o.compressed,
			Timeouts: fastls.Timeouts{Connect: o.connect},
		}, method)
		if e != nil {
			session.Close()
			var dns *net.DNSError
			if o.proxy != "" && errors.As(e, &dns) {
				p, _ := url.Parse(o.proxy)
				if strings.EqualFold(dns.Name, p.Hostname()) {
					return &failure{5, e}
				}
			}
			return e
		}
		if o.verbose {
			fmt.Fprintf(stderr, "< %s %d\n", resp.Protocol, resp.Status)
		}
		if dump != nil {
			e = writeHeaders(dump, resp)
		}
		if e == nil && (o.include || o.head) {
			e = writeHeaders(out, resp)
		}
		if e != nil {
			resp.Body.Close()
			session.Close()
			return &failure{23, e}
		}
		location := http.Header(resp.HeaderValues).Get("Location")
		if o.location && isRedirect(resp.Status) && location != "" {
			resp.Body.Close()
			session.Close()
			if hop >= o.maxRedirs {
				return fail(47, "maximum redirects exceeded")
			}
			next, e := target.Parse(location)
			if e != nil {
				return fail(3, "invalid redirect URL")
			}
			next.Fragment = ""
			next, e = parseURL(next.String())
			if e != nil {
				return e
			}
			if !sameOrigin(target, next) {
				// Suppression is permanent, including a later redirect back to origin.
				for _, name := range []string{"Authorization", "Cookie", "Host"} {
					delete(h, name)
					delete(base.Headers, name)
					delete(base.HeaderValues, name)
				}
				stripProfileCredentials(&base)
			}
			if resp.Status == 303 && method != "HEAD" || (resp.Status == 301 || resp.Status == 302) && method == "POST" {
				body = nil
				if o.method == "" {
					method = "GET"
				}
				for _, name := range []string{"Content-Type", "Content-Length", "Transfer-Encoding"} {
					h[name] = nil
				}
			}
			target = next
			continue
		}
		if !(o.fail && resp.Status >= 400) && !o.head {
			_, e = io.Copy(out, resp.Body)
		}
		resp.Body.Close()
		session.Close()
		if e != nil {
			var w *outputError
			if errors.As(e, &w) {
				return &failure{23, e}
			}
			return &failure{receiveCode(e), e}
		}
		if (o.fail || o.failBody) && resp.Status >= 400 {
			return fail(22, "HTTP response status %d", resp.Status)
		}
		if e = out.Close(); e != nil {
			return &failure{23, e}
		}
		if dump != nil {
			if e = dump.Close(); e != nil {
				return &failure{23, e}
			}
		}
		return nil
	}
}

func transportOptions(ctx context.Context, o options) (fastls.Options, error) {
	base := fastls.Options{VerifyTLS: !o.insecure, Proxy: o.proxy, Headers: map[string]string{}}
	if o.proxy != "" {
		if e := fastls.ValidateProxy(o.proxy); e != nil {
			return base, fail(2, "proxy configuration: %v", e)
		}
	}
	var err error
	if o.ca != "" {
		base.TLSRootCAs, err = readCA(o.ca)
		if err != nil {
			return base, err
		}
	}
	if o.proxyCA != "" {
		base.ProxyTLSRootCAs, err = readCA(o.proxyCA)
		if err != nil {
			return base, err
		}
	}
	switch {
	case o.profile != "":
		base.FingerprintProvider = generators.New()
		_, err = imitate.UseContext(ctx, &base, o.profile)
	case o.profileFile != "":
		var data []byte
		data, err = readLimitedFile(o.profileFile, 4<<20)
		if err == nil {
			var spec profile.Spec
			dec := json.NewDecoder(strings.NewReader(string(data)))
			dec.DisallowUnknownFields()
			err = dec.Decode(&spec)
			if err == nil {
				var extra any
				if e := dec.Decode(&extra); e != io.EOF {
					err = fmt.Errorf("profile file must contain exactly one JSON object")
				}
			}
			if err == nil {
				err = fastls.ApplyProfile(&base, spec)
			}
		}
	case o.ja3 != "":
		if strings.Count(o.ja3, ",") != 4 {
			return base, fail(2, "JA3 must have five comma-separated fields")
		}
		_, err = fastls.StringToSpec(o.ja3, "")
		base.Fingerprint = fastls.Ja3Fingerprint{FingerprintValue: o.ja3}
	case o.ja4r != "":
		_, err = fastls.ParseJA4R(o.ja4r, "")
		base.Fingerprint = fastls.Ja4Fingerprint{FingerprintValue: o.ja4r}
	default:
		base.Headers["User-Agent"] = "fcurl/" + Version
		base.Headers["Accept"] = "*/*"
	}
	if err != nil {
		return base, fail(2, "profile configuration: %v", err)
	}
	return base, nil
}

func parseURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u == nil || u.Hostname() == "" || u.Opaque != "" {
		return nil, fail(3, "invalid absolute URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fail(1, "only HTTP and HTTPS URLs are supported")
	}
	if u.User != nil {
		return nil, fail(3, "URL credentials are unsupported; supply Authorization explicitly")
	}
	u.Fragment = ""
	return u, nil
}
func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func stripProfileCredentials(o *fastls.Options) {
	if o.Profile == nil {
		return
	}
	s := profile.Clone(*o.Profile)
	filter := func(headers []profile.Header) []profile.Header {
		kept := headers[:0]
		for _, h := range headers {
			switch http.CanonicalHeaderKey(h.Name) {
			case "Authorization", "Cookie", "Host":
				continue
			}
			kept = append(kept, h)
		}
		return kept
	}
	s.Headers.Defaults = filter(s.Headers.Defaults)
	s.Headers.HTTP1Defaults = filter(s.Headers.HTTP1Defaults)
	s.Headers.HTTP2Defaults = filter(s.Headers.HTTP2Defaults)
	if s.Identity != nil {
		s.Identity.ClientHints = filter(s.Identity.ClientHints)
	}
	o.Profile = &s
}
func isRedirect(n int) bool { return n == 301 || n == 302 || n == 303 || n == 307 || n == 308 }
func sameFile(a, b string) bool {
	x, e1 := os.Stat(a)
	y, e2 := os.Stat(b)
	return e1 == nil && e2 == nil && os.SameFile(x, y)
}

const maxRequestBytes = 32 << 20

func readData(parts []dataPart, stdin io.Reader) ([]byte, string, bool, error) {
	var result []byte
	contentType := ""
	isJSON := false
	for _, p := range parts {
		b := []byte(p.value)
		if strings.HasPrefix(p.value, "@") {
			var err error
			if p.value == "@-" {
				b, err = io.ReadAll(io.LimitReader(stdin, maxRequestBytes+1))
			} else {
				b, err = readLimitedFile(p.value[1:], maxRequestBytes)
			}
			if err != nil {
				return nil, "", false, fail(26, "cannot read request data: %v", err)
			}
			if p.kind == "--data" {
				b = []byte(strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(string(b)))
			}
		}
		if p.kind == "--json" {
			isJSON = true
			contentType = "application/json"
		} else if contentType == "" {
			contentType = "application/x-www-form-urlencoded"
		}
		if len(result) > 0 && p.kind != "--json" {
			result = append(result, '&')
		}
		if len(b) > maxRequestBytes-len(result) {
			return nil, "", false, fail(26, "request data exceeds 32 MiB limit")
		}
		result = append(result, b...)
	}
	return result, contentType, isJSON, nil
}
func readLimitedFile(path string, max int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e == nil && int64(len(b)) > max {
		return nil, fmt.Errorf("file exceeds %d byte limit", max)
	}
	return b, e
}
func readCA(path string) (*x509.CertPool, error) {
	b, e := readLimitedFile(path, 4<<20)
	if e != nil {
		return nil, fail(77, "cannot read CA bundle: %v", e)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		return nil, fail(77, "CA bundle contains no certificates")
	}
	return pool, nil
}

func writeHeaders(w io.Writer, r fastls.Response) error {
	if _, e := fmt.Fprintf(w, "%s %d %s\r\n", r.Protocol, r.Status, http.StatusText(r.Status)); e != nil {
		return e
	}
	names := make([]string, 0, len(r.HeaderValues))
	for k := range r.HeaderValues {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		for _, v := range r.HeaderValues[k] {
			if _, e := fmt.Fprintf(w, "%s: %s\r\n", k, v); e != nil {
				return e
			}
		}
	}
	_, e := io.WriteString(w, "\r\n")
	return e
}

type outputError struct{ error }
type output struct {
	writer io.Writer
	closer io.Closer
	closed bool
}

func (w *output) Write(b []byte) (int, error) {
	n, e := w.writer.Write(b)
	if e == nil && n < len(b) {
		e = io.ErrShortWrite
	}
	if e != nil {
		return n, &outputError{e}
	}
	return n, nil
}
func (w *output) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if w.closer != nil {
		return w.closer.Close()
	}
	return nil
}
func openOutput(path string, fallback io.Writer) (io.WriteCloser, error) {
	if path == "" || path == "-" {
		return &output{writer: fallback}, nil
	}
	f, e := os.Create(path)
	if e != nil {
		return nil, fail(23, "cannot open output: %v", e)
	}
	return &output{writer: f, closer: f}, nil
}
func receiveCode(err error) int {
	code := errorCode(err)
	if code == 7 {
		return 56
	}
	return code
}
func errorCode(err error) int {
	var f *failure
	if errors.As(err, &f) {
		return f.code
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return 28
	}
	var cert x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &cert) || errors.As(err, &host) || errors.As(err, &invalid) {
		return 60
	}
	var verify *tls.CertificateVerificationError
	if errors.As(err, &verify) {
		return 60
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return 6
	}
	var record tls.RecordHeaderError
	if errors.As(err, &record) {
		return 35
	}
	var write *outputError
	if errors.As(err, &write) {
		return 23
	}
	var op *net.OpError
	if errors.As(err, &op) {
		if op.Op == "dial" {
			return 7
		}
		return 56
	}
	return 7
}
