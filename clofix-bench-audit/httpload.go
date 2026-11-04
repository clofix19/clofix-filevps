// Small HTTP load generator for isolated FileVPS / Docker nginx probes.
package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: httpload URL [seconds] [concurrency]")
		os.Exit(2)
	}
	url := os.Args[1]
	sec := 5
	n := 8
	if len(os.Args) > 2 {
		sec, _ = strconv.Atoi(os.Args[2])
	}
	if len(os.Args) > 3 {
		n, _ = strconv.Atoi(os.Args[3])
	}
	if sec < 1 {
		sec = 1
	}
	if n < 1 {
		n = 1
	}
	deadline := time.Now().Add(time.Duration(sec) * time.Second)
	var ok, fail atomic.Int64
	lat := make([]time.Duration, 0, 65536)
	var mu sync.Mutex
	var wg sync.WaitGroup
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   2 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          n * 2,
		MaxIdleConnsPerHost:   n * 2,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   2 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true,
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	runtime.GOMAXPROCS(runtime.NumCPU())
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				req, err := http.NewRequest(http.MethodGet, url, nil)
				if err != nil {
					fail.Add(1)
					continue
				}
				req.Header.Set("Connection", "keep-alive")
				req.Header.Set("Accept-Encoding", "identity")
				t0 := time.Now()
				resp, err := client.Do(req)
				d := time.Since(t0)
				if err != nil {
					fail.Add(1)
					time.Sleep(5 * time.Millisecond)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 400 {
					ok.Add(1)
					mu.Lock()
					lat = append(lat, d)
					mu.Unlock()
				} else {
					fail.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	p95 := 0.0
	if len(lat) > 0 {
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		p95 = float64(lat[len(lat)*95/100]) / float64(time.Millisecond)
	}
	elapsed := float64(sec)
	rps := float64(ok.Load()) / elapsed
	fmt.Printf("ok=%d fail=%d rps=%.1f p95_ms=%.1f conc=%d secs=%d\n", ok.Load(), fail.Load(), rps, p95, n, sec)
}
