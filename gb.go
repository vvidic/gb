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

func bench(url string, compress bool, timeout time.Duration, done chan struct{}, result chan stats) {
	s := stats{}
	s.code = make(map[int]int)

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
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Println(err)
	}

	written := 0
	buf := make([]byte, 10*1024)

LOOP:
	for {
		s.req++
		resp, err := client.Do(req)
		if err != nil {
			fmt.Println(err)
			s.err++
		} else {
			s.code[resp.StatusCode]++

			for {
				written, err = resp.Body.Read(buf)
				s.bytes += written
				if err != nil {
					break
				}
			}

			if err != nil && err != io.EOF {
				fmt.Println(err)
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

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			fmt.Println("Could not create cpu profile:", err)
			os.Exit(1)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	debug.SetGCPercent(*gcpercent)

	done := make(chan struct{})
	result := make(chan stats)

	fmt.Printf("Running %d parallel clients for %v...\n", *parallel, *duration)
	for i := 0; i < *parallel; i++ {
		go bench(url, *compression, *timeout, done, result)
	}

	time.Sleep(*duration)
	close(done)

	total := stats{}
	total.code = make(map[int]int)
	for i := 0; i < *parallel; i++ {
		s := <-result
		total.req += s.req
		total.err += s.err
		total.rerr += s.rerr
		total.bytes += s.bytes
		for k, v := range s.code {
			total.code[k] += v
		}
	}

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

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			fmt.Println("Could not create memory profile: ", err)
		}
		runtime.GC() // get up-to-date statistics
		if err := pprof.WriteHeapProfile(f); err != nil {
			fmt.Println("could not write memory profile: ", err)
		}
		f.Close()
	}
}
