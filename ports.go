package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const portRangeMin = 3000
const portRangeMax = 4000

// listeningPorts returns port→inode for TCP LISTEN sockets via /proc/net/tcp{,6}.
func listeningPorts() map[int]uint64 {
	ports := map[int]uint64{}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Scan() // skip header line
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 10 || fields[3] != "0A" { // 0A = TCP_LISTEN
				continue
			}
			// local_address field: "hexip:hexport"
			parts := strings.SplitN(fields[1], ":", 2)
			if len(parts) != 2 {
				continue
			}
			port, err := strconv.ParseInt(parts[1], 16, 32)
			if err != nil {
				continue
			}
			inode, err := strconv.ParseUint(fields[9], 10, 64)
			if err != nil {
				continue
			}
			ports[int(port)] = inode
		}
		f.Close()
	}
	return ports
}

type procInfo struct {
	PID  int
	Name string
}

// socketProcs maps each port to the process that owns its listening socket.
func socketProcs(portInodes map[int]uint64) map[int]procInfo {
	// build set of inodes we care about
	want := map[uint64]bool{}
	for _, inode := range portInodes {
		want[inode] = true
	}

	// scan /proc/<pid>/fd to find inode→pid
	inodePID := map[uint64]int{}
	procDir, err := os.Open("/proc")
	if err != nil {
		return nil
	}
	names, _ := procDir.Readdirnames(-1)
	procDir.Close()
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fmt.Sprintf("/proc/%d/fd", pid), fd.Name()))
			if err != nil {
				continue
			}
			var inode uint64
			if _, err := fmt.Sscanf(link, "socket:[%d]", &inode); err != nil {
				continue
			}
			if want[inode] {
				inodePID[inode] = pid
			}
		}
	}

	result := map[int]procInfo{}
	for port, inode := range portInodes {
		pid, ok := inodePID[inode]
		if !ok {
			continue
		}
		comm, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		result[port] = procInfo{PID: pid, Name: strings.TrimSpace(string(comm))}
	}
	return result
}

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
