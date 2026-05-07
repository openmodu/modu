package providers

import (
	"bufio"
	"io"
	"strings"
)

// ScanSSEData scans Server-Sent Events and calls handle for each "data:" payload.
func ScanSSEData(r io.Reader, handle func(data string) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		if !handle(data) {
			break
		}
	}
	return sc.Err()
}
