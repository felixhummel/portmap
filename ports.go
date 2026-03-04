package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const portRangeMin = 3000
const portRangeMax = 4000

type portBinding struct {
	Port  int
	Host  string
	Inode uint64
}

func parseIPv4Hex(s string) string {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 4 {
		return s
	}
	return net.IP{b[3], b[2], b[1], b[0]}.String()
}

func parseIPv6Hex(s string) string {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return s
	}
	ip := make(net.IP, 16)
	for i := 0; i < 4; i++ {
		ip[i*4+0] = b[i*4+3]
		ip[i*4+1] = b[i*4+2]
		ip[i*4+2] = b[i*4+1]
		ip[i*4+3] = b[i*4+0]
	}
	return ip.String()
}

// listeningPorts returns all TCP LISTEN socket bindings from /proc/net/tcp{,6}.
func listeningPorts() []portBinding {
	var bindings []portBinding
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		isV6 := strings.HasSuffix(path, "6")
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
			var host string
			if isV6 {
				host = parseIPv6Hex(parts[0])
			} else {
				host = parseIPv4Hex(parts[0])
			}
			bindings = append(bindings, portBinding{Port: int(port), Host: host, Inode: inode})
		}
		f.Close()
	}
	return bindings
}

type procInfo struct {
	PID  int
	Name string
}

// socketProcs maps each socket inode to the process that owns it.
func socketProcs(bindings []portBinding) map[uint64]procInfo {
	// build set of inodes we care about
	want := map[uint64]bool{}
	for _, b := range bindings {
		want[b.Inode] = true
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

	result := map[uint64]procInfo{}
	for inode, pid := range inodePID {
		comm, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		result[inode] = procInfo{PID: pid, Name: strings.TrimSpace(string(comm))}
	}
	return result
}

// dockerPorts queries the Docker socket for running containers with published
// ports. Returns a map from host port to container name. Fails silently if
// Docker is not available.
func dockerPorts() map[int]string {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
			},
		},
	}
	resp, err := client.Get("http://localhost/containers/json")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var containers []struct {
		Names []string `json:"Names"`
		Ports []struct {
			PublicPort uint16 `json:"PublicPort"`
			Type       string `json:"Type"`
		} `json:"Ports"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil
	}

	result := map[int]string{}
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		for _, p := range c.Ports {
			if p.Type == "tcp" && p.PublicPort != 0 {
				result[int(p.PublicPort)] = name
			}
		}
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
