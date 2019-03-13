package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"strings"
	"syscall"
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

type livestats struct {
	id    int
	req   int64
	bytes int64
}

func newStats() *stats {
	s := stats{}
	s.code = make(map[int]int64)
	s.hist = make(map[int]int64)

	return &s
}

func bench(id int, req *http.Request, client *http.Client,
	done <-chan struct{}, result chan<- *stats, errors chan<- error,
	rampch <-chan struct{}, livech chan<- livestats, tickch <-chan struct{}) {

	s := newStats()
	read := 0
	buf := make([]byte, 10*1024)

	var err error
	var resp *http.Response

	var t1, t2 time.Time
	var delta time.Duration
	var milisec int

	if rampch != nil {
		<-rampch
	}

	live := livestats{id: id}
	livets := time.Now()

LOOP:
	for {
		if tickch != nil {
			<-tickch
		}

		select {
		case <-done:
			break LOOP
		default:
		}

		s.req++
		live.req++
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
				live.bytes += int64(read)
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

		t2 = time.Now()
		delta = t2.Sub(t1)
		milisec = int(delta.Nanoseconds() / 1000000)
		s.hist[milisec]++

		if livech != nil && t2.Sub(livets) >= 500*time.Millisecond {
			livech <- live
			livets = t2
			live.req = 0
			live.bytes = 0
		}
	}

	result <- s
}

func disableRedirects(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}

func buildClient(compress bool, redirects bool, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DisableCompression:  !compress,
		TLSHandshakeTimeout: timeout,
		DialContext: (&net.Dialer{
			Timeout:   timeout,
			DualStack: true,
		}).DialContext,
	}

	redirectHandler := disableRedirects
	if redirects {
		redirectHandler = nil
	}

	client := &http.Client{
		CheckRedirect: redirectHandler,
		Transport:     transport,
		Timeout:       timeout,
	}

	return client
}

func buildRequest(method, url string) (*http.Request, error) {
	return http.NewRequest(method, url, nil)
}

func checkRequest(req *http.Request, client *http.Client) ([]string, error) {
	redirects := make([]string, 0)
	if client.CheckRedirect == nil {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			redirects = append(redirects, req.URL.String())
			if len(redirects) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return redirects, err
	}

	if resp.StatusCode >= http.StatusBadRequest { // 400
		return redirects, fmt.Errorf("%s", resp.Status)
	}

	return redirects, nil
}

func errorReporter(done <-chan struct{}, errors <-chan error) {
	var err error
	history := make(map[string]int)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

LOOP:
	for {
		select {
		case err = <-errors:
			history[err.Error()]++
		case <-ticker.C:
			for k, v := range history {
				fmt.Printf("Error: %s (%d)\n", k, v)
				delete(history, k)
			}
			runtime.GC()
		case <-done:
			break LOOP
		}
	}

	// workers might still be sending errors
	for range errors {
	}
}

func liveUpdates(done <-chan struct{}, livech <-chan livestats, duration time.Duration) {
	var sum, live livestats
	start := time.Now()

	var t time.Time
	var elapsed time.Duration
	var percent int64

	var ccount int
	clients := make(map[int]int)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

LOOP:
	for {
		select {
		case live = <-livech:
			sum.req += live.req
			sum.bytes += live.bytes
			clients[live.id]++
		case t = <-ticker.C:
			elapsed = t.Sub(start)
			percent = elapsed.Nanoseconds() * 100 / duration.Nanoseconds()
			ccount = 0
			for k := range clients {
				if clients[k] > 0 {
					ccount++
				}
				clients[k] = 0
			}

			if percent < 100 {
				fmt.Printf("Duration: %3.0fs (%2d%%) | Rate: %d req/s "+
					"| Throughput: %s | Live clients: %d\n",
					elapsed.Seconds(), percent, sum.req,
					reportThroughput(sum.bytes, time.Second), ccount)
			}
			sum.req = 0
			sum.bytes = 0
			ccount = 0
		case <-done:
			break LOOP
		}
	}

	// workers might still be sending livestats
	for range livech {
	}
}

func rampupGenerator(rampch chan<- struct{}, done <-chan struct{}, n int, total time.Duration) {
	if total == 0 {
		close(rampch)
		return
	}

	d := 100 * time.Millisecond
	ticker := time.NewTicker(d)
	defer ticker.Stop()

	groups := int(total / d)
	groupCnt := n / groups

LOOP:
	for {
		for i := 0; i < groupCnt; i++ {
			rampch <- struct{}{}

			n--
			if n == 0 {
				break LOOP
			}
		}

		select {
		case <-ticker.C:
		case <-done:
			break LOOP
		}
	}

	close(rampch)
}

func rateTicker(rate int, done <-chan struct{}) <-chan struct{} {
	if rate <= 0 {
		return nil
	}

	tickch := make(chan struct{})

	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(rate))

	LOOP:
		for {
			select {
			case <-ticker.C:
				tickch <- struct{}{}
			case <-done:
				break LOOP
			}
		}

		ticker.Stop()
		close(tickch)
	}()

	return tickch
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
	gwidth := 60 - mwidth - cwidth

	for _, m := range milis {
		s1 := fmt.Sprintf("Time[%*d ms]: %*d", mwidth, m, cwidth, total.hist[m])
		s2 := "     "

		if next < len(want) {
			sum += total.hist[m]
			percentile = sum * 100 / total.req

			i := next
			for i < len(want) && percentile >= want[i] {
				i++
			}
			i--

			if i >= next {
				s2 = fmt.Sprintf("(%d%%)", want[i])
				next = i + 1
			}
		}

		stars := total.hist[m] * int64(gwidth) / cmax
		s3 := "|" + strings.Repeat("*", int(stars))

		fmt.Println(s1, s2, s3)
	}
}

func reportStats(total *stats, duration time.Duration, histogram bool) {
	fmt.Println()
	fmt.Printf("Duration: %.2fs\n", duration.Seconds())
	fmt.Println("Requests:", total.req)
	fmt.Printf("Rate: %.2f req/s\n", float64(total.req)/duration.Seconds())
	fmt.Printf("Size: %s (%s/req)\n", reportSize(total.bytes),
		reportSize(total.bytes/total.req))
	fmt.Println("Throughput:", reportThroughput(total.bytes, duration))
	fmt.Println("Bandwidth:", reportBandwidth(total.bytes, duration))
	if total.err > 0 {
		fmt.Println("Connection errors:", total.err)
	}
	if total.rerr > 0 {
		fmt.Println("Read errors:", total.rerr)
	}

	reportStatus(total)

	if histogram {
		reportHistogram(total)
	}
}

func updateRlimit(parallel int) error {
	var val syscall.Rlimit

	err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &val)
	if err != nil {
		return err
	}

	want := uint64(parallel) + 32
	if val.Cur >= want {
		return nil
	}

	val.Max = want
	val.Cur = want

	return syscall.Setrlimit(syscall.RLIMIT_NOFILE, &val)
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

type flags struct {
	compression bool
	cpuprofile  string
	duration    time.Duration
	gcpercent   int
	histogram   bool
	live        bool
	memprofile  string
	parallel    int
	rampup      time.Duration
	rate        int
	redirects   bool
	timeout     time.Duration
}

func printUsage() {
	output := flag.CommandLine.Output()
	fmt.Fprintf(output, "Usage: gb [options] <url>\n\nOptions:\n")
	flag.PrintDefaults()
}

func parseFlags() *flags {
	f := flags{}

	flag.BoolVar(&f.compression, "compression", true, "use HTTP compression")
	flag.StringVar(&f.cpuprofile, "cpuprofile", "", "write cpu profile to file")
	flag.DurationVar(&f.duration, "duration", 15*time.Second, "test duration")
	flag.IntVar(&f.gcpercent, "gcpercent", 1000, "garbage collection target percentage")
	flag.BoolVar(&f.histogram, "histogram", false, "display response time histogram")
	flag.BoolVar(&f.live, "live", false, "show periodic progress updates")
	flag.StringVar(&f.memprofile, "memprofile", "", "write memory profile to file")
	flag.IntVar(&f.parallel, "parallel", 20, "number of parallel client connections")
	flag.DurationVar(&f.rampup, "rampup", 0, "startup interval for client connections")
	flag.IntVar(&f.rate, "rate", 0, "limit rate (requests per second)")
	flag.BoolVar(&f.redirects, "redirects", true, "follow HTTP redirects")
	flag.DurationVar(&f.timeout, "timeout", 10*time.Second, "request timeout")

	flag.Usage = printUsage
	flag.Parse()

	return &f
}

func main() {
	f := parseFlags()

	url := flag.Arg(0)
	if url == "" {
		fmt.Printf("Error: No url given\n\n")
		printUsage()
		os.Exit(1)
	}

	if startCPUProfile(f.cpuprofile) {
		defer stopCPUProfile()
	}

	debug.SetGCPercent(f.gcpercent)

	done := make(chan struct{})
	result := make(chan *stats)
	errors := make(chan error)

	var livech chan livestats
	if f.live {
		size := f.parallel
		if size > 1000 {
			size = 1000
		}
		livech = make(chan livestats, size)
	}

	rampch := make(chan struct{})
	tickch := rateTicker(f.rate, done)

	req, err := buildRequest(http.MethodGet, url)
	if err != nil {
		fmt.Printf("Invalid url %s: %s\n", url, err)
		os.Exit(1)
	}

	redirs, err := checkRequest(req, buildClient(f.compression, f.redirects, f.timeout))
	if err != nil {
		fmt.Printf("Url check failed for %s: %s\n", url, err)
		os.Exit(1)
	}
	if len(redirs) > 0 {
		fmt.Printf("Warning: redirects detected: %s -> %s\n", url, strings.Join(redirs, " -> "))
	}

	if err = updateRlimit(f.parallel); err != nil {
		fmt.Println("Warning: failed to update rlimit:", err)
	}

	fmt.Printf("Running %d parallel clients for %v...\n", f.parallel, f.duration)
	for i := 0; i < f.parallel; i++ {
		cli := buildClient(f.compression, f.redirects, f.timeout)
		go bench(i+1, req, cli, done, result, errors, rampch, livech, tickch)
	}

	if f.live {
		go liveUpdates(done, livech, f.duration)
	}
	go errorReporter(done, errors)
	go rampupGenerator(rampch, done, f.parallel, f.rampup)

	t1 := time.Now()
	intr := make(chan os.Signal, 1)
	signal.Notify(intr, os.Interrupt)
	timer := time.NewTimer(f.duration)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-intr:
	}

	fmt.Println("Stopping clients and collecting results...")
	close(done)

	delta := time.Since(t1)
	total := collectStats(result, f.parallel)
	reportStats(total, delta, f.histogram)

	close(errors)
	if livech != nil {
		close(livech)
	}

	writeMemProfile(f.memprofile)
}
