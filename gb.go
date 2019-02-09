package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"time"
)

type stats struct {
	req   int64         // requests
	err   int64         // connection errors
	rerr  int64         // read errors
	bytes int64         // bytes read
	code  map[int]int64 // status code counts
	hist  map[int]int64 // response time histogram
}

func newStats() *stats {
	s := stats{}
	s.code = make(map[int]int64)
	s.hist = make(map[int]int64)

	return &s
}

func bench(req *http.Request, client *http.Client,
	done <-chan struct{}, result chan<- *stats, errors chan<- error) {

	s := newStats()

	read := 0
	buf := make([]byte, 10*1024)

	var err error
	var resp *http.Response

	var t1 time.Time
	var delta time.Duration
	var milisec int

LOOP:
	for {
		s.req++
		t1 = time.Now()
		resp, err = client.Do(req)
		if err != nil {
			errors <- fmt.Errorf("request failed: %s", err)
			s.err++
		} else {
			s.code[resp.StatusCode]++

			for {
				read, err = resp.Body.Read(buf)
				s.bytes += int64(read)
				if err != nil {
					break
				}
			}

			if err != nil && err != io.EOF {
				errors <- fmt.Errorf("read failed: %s", err)
				s.rerr++
			}

			resp.Body.Close()
		}

		delta = time.Since(t1)
		milisec = int(delta.Nanoseconds() / 1000000)
		s.hist[milisec]++

		select {
		case <-done:
			break LOOP
		default:
		}
	}

	result <- s
}

func buildClient(compress bool, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DisableCompression:  !compress,
		TLSHandshakeTimeout: timeout,
		DialContext: (&net.Dialer{
			Timeout:   timeout,
			DualStack: true,
		}).DialContext,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	return client
}

func buildRequest(method, url string) (*http.Request, error) {
	return http.NewRequest(method, url, nil)
}

func checkRequest(req *http.Request, client *http.Client) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode >= http.StatusBadRequest { // 400
		return fmt.Errorf("%s", resp.Status)
	}

	return nil
}

func errorReporter(done <-chan struct{}, errors <-chan error) {
	var err error
	hist := make(map[string]int)
	ticker := time.NewTicker(1 * time.Second)

LOOP:
	for {
		select {
		case err = <-errors:
			hist[err.Error()]++
		case <-ticker.C:
			for k, v := range hist {
				fmt.Printf("Error: %s (%d)\n", k, v)
				delete(hist, k)
			}
		case <-done:
			break LOOP
		default:
		}
	}

	ticker.Stop()
}

func collectStats(result <-chan *stats, n int) *stats {
	total := newStats()

	for i := 0; i < n; i++ {
		s := <-result
		total.req += s.req
		total.err += s.err
		total.rerr += s.rerr
		total.bytes += s.bytes
		for k, v := range s.code {
			total.code[k] += v
		}
		for k, v := range s.hist {
			total.hist[k] += v
		}
	}

	return total
}

func reportSize(n int64) string {
	units := []string{"B", "kB", "MB", "GB", "TB", "PB"}

	var i int
	m := float64(n)

	for i = 0; i < len(units); i++ {
		if m < 1024 {
			break
		}

		m /= 1024
	}

	return fmt.Sprintf("%.2f %s", m, units[i])
}

func reportThroughput(n int64, duration time.Duration) string {
	units := []string{"B/s", "kB/s", "MB/s", "GB/s", "TB/s", "PB/s"}

	var i int
	m := float64(n) / duration.Seconds()

	for i = 0; i < len(units); i++ {
		if m < 1024 {
			break
		}

		m /= 1024
	}
	return fmt.Sprintf("%.2f %s", m, units[i])
}

func reportBandwidth(n int64, duration time.Duration) string {
	units := []string{"bps", "kbps", "Mbps", "Gbps"}

	var i int
	m := float64(8*n) / duration.Seconds()

	for i = 0; i < len(units); i++ {
		if m < 1000 {
			break
		}

		m /= 1000
	}
	return fmt.Sprintf("%.2f %s", m, units[i])
}

func reportStatus(total *stats) {
	if len(total.code) == 0 {
		return
	}

	fmt.Println()

	codes := make([]int, 0, len(total.code))
	for c := range total.code {
		codes = append(codes, c)
	}
	sort.Ints(codes)

	for _, c := range codes {
		fmt.Printf("Status[%d]: %d\n", c, total.code[c])
	}
}

func reportHistogram(total *stats) {
	if len(total.hist) == 0 {
		return
	}

	fmt.Println()

	milis := make([]int, 0, len(total.hist))
	var cmax int64
	for t, c := range total.hist {
		milis = append(milis, t)
		if c > cmax {
			cmax = c
		}
	}
	sort.Ints(milis)

	mwidth := len(fmt.Sprintf("%d", milis[len(milis)-1]))
	cwidth := len(fmt.Sprintf("%d", cmax))

	var sum, percentile int64
	want := []int64{10, 25, 50, 75, 90, 95, 99}
	next := 0

	for _, m := range milis {
		fmt.Printf("Time[%*d ms]: %*d", mwidth, m, cwidth, total.hist[m])

		if next < len(want) {
			sum += total.hist[m]
			percentile = sum * 100 / total.req

			i := next
			for i < len(want) && percentile >= want[i] {
				i++
			}
			i--

			if i >= next {
				fmt.Printf(" (%d%%)", want[i])
				next = i + 1
			}
		}

		fmt.Println()
	}
}

func reportStats(total *stats, duration time.Duration, histogram bool) {
	fmt.Println()
	fmt.Printf("Duration: %.2fs\n", duration.Seconds())
	fmt.Println("Requests:", total.req)
	fmt.Printf("Rate: %.2f req/s\n", float64(total.req)/duration.Seconds())
	fmt.Println("Size:", reportSize(total.bytes))
	fmt.Println("Bandwidth:", reportBandwidth(total.bytes, duration))
	fmt.Println("Throughput:", reportThroughput(total.bytes, duration))
	fmt.Println("Size/Request:", reportSize(total.bytes/total.req))
	if total.err > 0 {
		fmt.Println("Errors:", total.err)
	}
	if total.rerr > 0 {
		fmt.Println("Read errors:", total.rerr)
	}

	reportStatus(total)

	if histogram {
		reportHistogram(total)
	}
}

func startCPUProfile(filename string) bool {
	if filename == "" {
		return false
	}

	f, err := os.Create(filename)
	if err != nil {
		fmt.Println("Could not create cpu profile:", err)
		os.Exit(1)
	}

	err = pprof.StartCPUProfile(f)
	if err != nil {
		fmt.Println("Could not start cpu profile:", err)
		os.Exit(1)
	}

	return true
}

func stopCPUProfile() {
	pprof.StopCPUProfile()
}

func writeMemProfile(filename string) {
	if filename == "" {
		return
	}

	f, err := os.Create(filename)
	if err != nil {
		fmt.Println("Could not create memory profile: ", err)
	}
	defer f.Close()

	runtime.GC() // get up-to-date statistics
	if err := pprof.WriteHeapProfile(f); err != nil {
		fmt.Println("could not write memory profile: ", err)
	}
}

func main() {
	var compression = flag.Bool("compression", true, "use HTTP compression")
	var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")
	var duration = flag.Duration("duration", 15*time.Second, "test duration")
	var gcpercent = flag.Int("gcpercent", 1000, "garbage collection target percentage")
	var histogram = flag.Bool("histogram", false, "display response time histogram")
	var memprofile = flag.String("memprofile", "", "write memory profile to file")
	var parallel = flag.Int("parallel", 20, "number of parallel client connections")
	var timeout = flag.Duration("timeout", 10*time.Second, "request timeout")

	flag.Parse()
	url := flag.Arg(0)
	if url == "" {
		fmt.Println("No url given")
		os.Exit(1)
	}

	if startCPUProfile(*cpuprofile) {
		defer stopCPUProfile()
	}

	debug.SetGCPercent(*gcpercent)

	done := make(chan struct{})
	result := make(chan *stats)
	errors := make(chan error)

	req, err := buildRequest(http.MethodGet, url)
	if err != nil {
		fmt.Printf("Invalid url %s: %s\n", url, err)
		os.Exit(1)
	}

	err = checkRequest(req, buildClient(*compression, *timeout))
	if err != nil {
		fmt.Printf("Url check failed for %s: %s\n", url, err)
		os.Exit(1)
	}

	t1 := time.Now()
	fmt.Printf("Running %d parallel clients for %v...\n", *parallel, *duration)
	for i := 0; i < *parallel; i++ {
		cli := buildClient(*compression, *timeout)
		go bench(req, cli, done, result, errors)
	}
	go errorReporter(done, errors)

	time.Sleep(*duration)
	fmt.Println("Stopping clients and collecting results...")
	close(done)

	delta := time.Since(t1)
	total := collectStats(result, *parallel)
	reportStats(total, delta, *histogram)

	writeMemProfile(*memprofile)
}
