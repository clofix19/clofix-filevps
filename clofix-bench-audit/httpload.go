// Small HTTP load generator for isolated FileVPS nginx probes.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
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
	lat := make([]time.Duration, 0, 4096)
	var mu sync.Mutex
	var wg sync.WaitGroup
	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				t0 := time.Now()
				resp, err := client.Get(url)
				d := time.Since(t0)
				if err != nil {
					fail.Add(1)
					time.Sleep(20 * time.Millisecond)
					continue
				}
				io.Copy(io.Discard, resp.Body)
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
	rps := float64(ok.Load()) / float64(sec)
	fmt.Printf("ok=%d fail=%d rps=%.1f p95_ms=%.1f\n", ok.Load(), fail.Load(), rps, p95)
}
