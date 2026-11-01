package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runResourceSoak(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "bounded", Path: "/", HttpOnly: true})
		io.WriteString(w, strings.Repeat("x", 64<<10))
	}))
	defer s.Close()
	jar := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(jar, []byte("# Netscape HTTP Cookie File\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func() {
		if code := Run(context.Background(), []string{"-b", jar, "-c", jar, s.URL}, strings.NewReader(""), io.Discard, io.Discard); code != 0 {
			t.Fatalf("resource request exit %d", code)
		}
	}
	for i := 0; i < 20; i++ {
		run()
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	goroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(5 * time.Minute)
	count := 0
	for time.Now().Before(deadline) {
		run()
		count++
		time.Sleep(50 * time.Millisecond)
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	// Acceptance budgets cover retained CLI allocations and background workers;
	// this is a growth check, not a throughput or browser-fidelity certification.
	if after.HeapAlloc > before.HeapAlloc+(32<<20) {
		t.Errorf("retained heap grew beyond 32 MiB: %d -> %d", before.HeapAlloc, after.HeapAlloc)
	}
	if n := runtime.NumGoroutine(); n > goroutines+16 {
		t.Errorf("goroutine growth beyond 16: %d -> %d", goroutines, n)
	}
	t.Logf("5 minute resource check: requests=%d heap=%d->%d goroutines=%d->%d", count, before.HeapAlloc, after.HeapAlloc, goroutines, runtime.NumGoroutine())
}
