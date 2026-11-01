// Tiny anonymous-memory holder used by the isolated OOM probe.
package main

import "os"

func main() {
	b := make([]byte, 300<<20)
	for i := range b {
		b[i] = 1
	}
	_, _ = os.Stdout.WriteString("held\n")
	select {}
}
