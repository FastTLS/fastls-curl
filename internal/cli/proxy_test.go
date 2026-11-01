package cli

import (
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestHTTPSProxyTrustIsSeparate(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "via-tls-proxy") }))
	defer origin.Close()
	var wg sync.WaitGroup
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpwYXNz" {
			http.Error(w, "proxy authentication required", 407)
			return
		}
		up, e := net.Dial("tcp", r.Host)
		if e != nil {
			http.Error(w, "dial", 502)
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
	proxyURL := strings.Replace(proxy.URL, "https://", "https://user:pass@", 1)
	if c, _, e := invoke("-k", "-x", proxyURL, origin.URL); c != 60 || strings.Contains(e, "user:pass") {
		t.Fatalf("origin -k weakened proxy verification: %d %s", c, e)
	}
	ca := filepath.Join(t.TempDir(), "proxy-ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0600)
	if c, out, e := invoke("-x", proxyURL, "--proxy-cacert", ca, origin.URL); c != 0 || out != "via-tls-proxy" {
		t.Fatalf("%d %q %s", c, out, e)
	}
}

func TestSOCKSDNSModes(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "socks-ok") }))
	defer origin.Close()
	for _, tc := range []struct {
		scheme string
		atyp   byte
	}{{"socks5", 1}, {"socks5h", 3}} {
		t.Run(tc.scheme, func(t *testing.T) {
			ln, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer ln.Close()
			result := make(chan error, 1)
			go func() {
				c, e := ln.Accept()
				if e != nil {
					result <- e
					return
				}
				defer c.Close()
				header := make([]byte, 2)
				if _, e = io.ReadFull(c, header); e != nil {
					result <- e
					return
				}
				methods := make([]byte, int(header[1]))
				io.ReadFull(c, methods)
				c.Write([]byte{5, 0})
				request := make([]byte, 4)
				if _, e = io.ReadFull(c, request); e != nil {
					result <- e
					return
				}
				if request[3] != tc.atyp {
					result <- fmt.Errorf("address type %d, want %d", request[3], tc.atyp)
					return
				}
				var host string
				switch request[3] {
				case 1:
					b := make([]byte, 4)
					io.ReadFull(c, b)
					host = net.IP(b).String()
				case 3:
					b := make([]byte, 1)
					io.ReadFull(c, b)
					name := make([]byte, int(b[0]))
					io.ReadFull(c, name)
					host = string(name)
				}
				port := make([]byte, 2)
				io.ReadFull(c, port)
				up, e := net.Dial("tcp", net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(port))))
				if e != nil {
					result <- e
					return
				}
				defer up.Close()
				c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				done := make(chan struct{})
				go func() { io.Copy(up, c); up.Close(); close(done) }()
				io.Copy(c, up)
				c.Close()
				<-done
				result <- nil
			}()
			// localhost is resolved locally only for socks5. The origin listens on IPv4.
			target := strings.Replace(origin.URL, "127.0.0.1", "localhost", 1)
			c, out, diag := invoke("-x", tc.scheme+"://"+ln.Addr().String(), target)
			if c != 0 || out != "socks-ok" {
				t.Errorf("%d %q %s", c, out, diag)
			}
			if e := <-result; e != nil {
				t.Fatal(e)
			}
		})
	}
}
