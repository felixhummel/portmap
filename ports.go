package main

import (
	"net"
	"strconv"
)

const portRangeMin = 3000
const portRangeMax = 4000

func boundPorts() map[int]bool {
	bound := map[int]bool{}
	for port := 1; port <= 65535; port++ {
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			bound[port] = true
			continue
		}
		ln.Close()
	}
	return bound
}

func allocate(entries []Entry) (int, bool) {
	registered := map[int]bool{}
	for _, e := range entries {
		registered[e.Port] = true
	}

	for port := portRangeMin; port <= portRangeMax; port++ {
		if registered[port] {
			continue
		}
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			continue
		}
		ln.Close()
		return port, true
	}
	return 0, false
}
