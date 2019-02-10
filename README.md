# gb
Go HTTP server benchmarking tool

By default gb starts 20 goroutines, each opening a single connection
to the target server and sends requests in a loop for 15 seconds.
After the test is finished results from all goroutines are merged
and reported.

Example usage:

```
$ gb https://some.web.site/path
Running 20 parallel clients for 15s...
Stopping clients and collecting results...

Duration: 15.00s
Requests: 7233
Rate: 482.19 req/s
Size: 413.57 MB (58.55 kB/req)
Throughput: 27.57 MB/s
Bandwidth: 231.28 Mbps

Status[200]: 7233
```

Available command line options:

```
$ gb -help
Usage: gb [options] <url>

Options:
  -compression
        use HTTP compression (default true)
  -cpuprofile string
        write cpu profile to file
  -duration duration
        test duration (default 15s)
  -gcpercent int
        garbage collection target percentage (default 1000)
  -histogram
        display response time histogram
  -memprofile string
        write memory profile to file
  -parallel int
        number of parallel client connections (default 20)
  -rampup duration
        startup interval for client connections
  -rate int
        limit the rate of requests per second
  -redirects
        follow HTTP redirects (default true)
  -timeout duration
        request timeout (default 10s)
```
