# gb
Go HTTP server benchmarking tool

By default gb starts 20 goroutines, each opening a single connection
to the target server and sends requests in a loop for 15 seconds.
After the test is finished results from all goroutines are merged
and reported.

Example usage:

```
$ gb -histogram https://some.web.site/path
Running 20 parallel clients for 15s...
Stopping clients and collecting results...

Duration: 15.00s
Requests: 14700
Rate: 979.86 req/s
Size: 59.43 MB (4.14 kB/req)
Throughput: 3.96 MB/s
Bandwidth: 33.23 Mbps

Status[200]: 14700

Time[ 13 ms]:    2       |
Time[ 14 ms]:   58       |*
Time[ 15 ms]:  314       |*****
Time[ 16 ms]:  765       |**************
Time[ 17 ms]: 1301 (10%) |************************
Time[ 18 ms]: 2162 (25%) |****************************************
Time[ 19 ms]: 2835 (50%) |*****************************************************
Time[ 20 ms]: 2526       |***********************************************
Time[ 21 ms]: 1747 (75%) |********************************
Time[ 22 ms]: 1075       |********************
Time[ 23 ms]:  710 (90%) |*************
Time[ 24 ms]:  434       |********
Time[ 25 ms]:  267 (95%) |****
Time[ 26 ms]:  171       |***
Time[ 27 ms]:   91       |*
Time[ 28 ms]:   65       |*
Time[ 29 ms]:   40 (99%) |
Time[ 30 ms]:   31       |
Time[ 31 ms]:   25       |
Time[ 32 ms]:    8       |
Time[ 33 ms]:   10       |
Time[ 34 ms]:    7       |
Time[ 35 ms]:    4       |
Time[ 36 ms]:    4       |
Time[ 37 ms]:    6       |
Time[ 38 ms]:    3       |
Time[ 39 ms]:    3       |
Time[ 40 ms]:    3       |
Time[ 41 ms]:    1       |
Time[ 42 ms]:    3       |
Time[ 43 ms]:    2       |
Time[ 44 ms]:    3       |
Time[ 45 ms]:    1       |
Time[ 46 ms]:    1       |
Time[ 47 ms]:    1       |
Time[ 55 ms]:    1       |
Time[ 94 ms]:    2       |
Time[ 95 ms]:    4       |
Time[ 99 ms]:    1       |
Time[100 ms]:    5       |
Time[102 ms]:    2       |
Time[103 ms]:    1       |
Time[104 ms]:    2       |
Time[105 ms]:    2       |
Time[106 ms]:    1       |
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
