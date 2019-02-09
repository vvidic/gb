# gb
Go HTTP server benchmarking tool

By default gb starts 20 gorutines, each opening a single connection
to the target server and sends requests in a loop for 15 seconds.
After the test is finished results from all gorutines are merged
and reported.

Example usage:

```
$ gb https://some.web.site/path
Running 20 parallel clients for 15s...
Stopping clients and collecting results...

Duration: 15.00s
Requests: 7233
Rate: 482.19 req/s
Size: 413.57 MB
Bandwidth: 231.28 Mbps
Throughput: 27.57 MB/s
Size/Request: 58.55 kB

Status[200]: 7233
```

Available command line options:

```
$ gb -help
Usage of gb:
  -compression
        use HTTP compression (default true)
  -cpuprofile string
        write cpu profile to file
  -duration duration
        test duration (default 15s)
  -gcpercent int
        garbage collection target percentage (default 1000)
  -memprofile string
        write memory profile to file
  -parallel int
        number of parallel client connections (default 20)
  -timeout duration
        request timeout (default 10s)
```
