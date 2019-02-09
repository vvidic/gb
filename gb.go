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
	"time"
)

type stats struct {
	req   int
	err   int
	rerr  int
	bytes int
	code  map[int]int
}

func bench(req *http.Request, client *http.Client,
	done <-chan struct{}, result chan<- stats, errors chan<- error) {

	s := stats{}
	s.code = make(map[int]int)

	read := 0
	buf := make([]byte, 10*1024)

	var err error
	var resp *http.Response

LOOP:
	for {
		s.req++
		resp, err = client.Do(req)
		if err != nil {
			errors <- fmt.Errorf("request failed: %s", err)
			s.err++
		} else {
			s.code[resp.StatusCode]++

			for {
				read, err = resp.Body.Read(buf)
				s.bytes += read
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

func collectStats(result <-chan stats, n int) stats {
	total := stats{}
	total.code = make(map[int]int)
	for i := 0; i < n; i++ {
		s := <-result
		total.req += s.req
		total.err += s.err
		total.rerr += s.rerr
		total.bytes += s.bytes
		for k, v := range s.code {
			total.code[k] += v
		}
	}

	return total
}

func reportStats(total stats, duration time.Duration) {
	fmt.Println("Requests:", total.req)
	fmt.Printf("Rate: %.1f/s\n", float64(total.req)/duration.Seconds())
	fmt.Println("Bytes:", total.bytes)
	if total.err > 0 {
		fmt.Println("Errors:", total.err)
	}
	if total.rerr > 0 {
		fmt.Println("Read errors:", total.rerr)
	}
	for k, v := range total.code {
		fmt.Printf("Code[%d]: %d\n", k, v)
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
	result := make(chan stats)
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

	fmt.Printf("Running %d parallel clients for %v...\n", *parallel, *duration)
	for i := 0; i < *parallel; i++ {
		cli := buildClient(*compression, *timeout)
		go bench(req, cli, done, result, errors)
	}
	go errorReporter(done, errors)

	time.Sleep(*duration)
	close(done)

	total := collectStats(result, *parallel)
	reportStats(total, *duration)

	writeMemProfile(*memprofile)
}
