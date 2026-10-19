package ports

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProcessInfo identifies a listener. CommandLine is informational, never executed.
type ProcessInfo struct {
	PID         int
	Name        string
	CommandLine string
	identity    string
}

// ErrProcessInaccessible means a TCP listener exists but its owner cannot be
// established safely from procfs. Callers must never attempt to kill it.
var ErrProcessInaccessible = errors.New("listener belongs to an inaccessible external system process")

type tcpListener struct {
	inode, network, address string
	port                    int
}

func tcpListeners() ([]tcpListener, error) {
	var result []tcpListener
	for _, network := range []string{"tcp", "tcp6"} {
		f, err := os.Open("/proc/net/" + network)
		if err != nil {
			return nil, fmt.Errorf("TCP inspection requires readable Linux /proc: %w", err)
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			addr := strings.Split(fields[1], ":")
			if len(addr) != 2 {
				continue
			}
			port, err := strconv.ParseInt(addr[1], 16, 32)
			if err != nil {
				continue
			}
			ip, err := hex.DecodeString(addr[0])
			if err != nil || (len(ip) != 4 && len(ip) != 16) {
				continue
			}
			// procfs prints each native-endian 32-bit word in hexadecimal (Linux x86/ARM LE).
			for i := 0; i < len(ip); i += 4 {
				ip[i], ip[i+3] = ip[i+3], ip[i]
				ip[i+1], ip[i+2] = ip[i+2], ip[i+1]
			}
			netw := "tcp4"
			if network == "tcp6" {
				netw = "tcp6"
			}
			result = append(result, tcpListener{fields[9], netw, net.JoinHostPort(net.IP(ip).String(), strconv.Itoa(int(port))), int(port)})
		}
		err = scanner.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func processIdentity(pid int) (string, bool, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", false, err
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", false, fmt.Errorf("invalid process stat for PID %d", pid)
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 20 {
		return "", false, fmt.Errorf("incomplete process stat for PID %d", pid)
	}
	return fields[19], fields[0] == "Z", nil
}

func socketInodes(pid int) (map[string]bool, error) {
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool)
	var permissionErr error
	for _, entry := range entries {
		link, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, entry.Name()))
		if errors.Is(err, os.ErrPermission) {
			permissionErr = err
		}
		if err == nil && strings.HasPrefix(link, "socket:[") {
			result[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = true
		}
	}
	if permissionErr != nil {
		return nil, permissionErr
	}
	return result, nil
}

// FindProcessByPort returns one unambiguous TCP listener owner, or an error.
// Inaccessible or shared listeners fail closed rather than guessing an owner.
func FindProcessByPort(port int) (*ProcessInfo, error) {
	owners, err := FindProcessesByPort(port)
	if err != nil {
		return nil, err
	}
	if len(owners) != 1 {
		return nil, fmt.Errorf("port %d has multiple listener owners; use multi-process resolution", port)
	}
	return owners[0], nil
}

// FindProcessesByPort returns every visible owner, ordered by PID. Missing socket
// ownership fails closed: callers must not terminate a partially identified set.
func FindProcessesByPort(port int) ([]*ProcessInfo, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid TCP port %d", port)
	}
	listeners, err := tcpListeners()
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool)
	for _, l := range listeners {
		if l.port == port {
			wanted[l.inode] = true
		}
	}
	if len(wanted) == 0 {
		return nil, fmt.Errorf("no TCP listener found on port %d", port)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("port %d: %w", port, ErrProcessInaccessible)
		}
		return nil, err
	}
	return findProcessOwners(port, wanted, entries, socketInodes)
}

func findProcessOwner(port int, wanted map[string]bool, entries []os.DirEntry, scan func(int) (map[string]bool, error)) (*ProcessInfo, error) {
	owners, err := findProcessOwners(port, wanted, entries, scan)
	if err != nil {
		return nil, err
	}
	if len(owners) != 1 {
		return nil, fmt.Errorf("port %d has multiple listener owners", port)
	}
	return owners[0], nil
}

func findProcessOwners(port int, wanted map[string]bool, entries []os.DirEntry, scan func(int) (map[string]bool, error)) ([]*ProcessInfo, error) {
	var found []*ProcessInfo
	matched := make(map[string]bool)
	inaccessible := false
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		inodes, err := scan(pid)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				inaccessible = true
			}
			continue
		}
		owns := false
		for inode := range inodes {
			if wanted[inode] {
				owns = true
				matched[inode] = true
			}
		}
		if !owns {
			continue
		}
		identity, _, err := processIdentity(pid)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil, fmt.Errorf("port %d: %w", port, ErrProcessInaccessible)
			}
			return nil, err
		}
		name, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil, fmt.Errorf("port %d: %w", port, ErrProcessInaccessible)
			}
			return nil, err
		}
		command, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil, fmt.Errorf("port %d: %w", port, ErrProcessInaccessible)
			}
			return nil, err
		}
		found = append(found, &ProcessInfo{PID: pid, Name: strings.TrimSpace(string(name)), CommandLine: strings.TrimSpace(strings.ReplaceAll(string(command), "\x00", " ")), identity: identity})
	}
	if len(found) == 0 || len(matched) != len(wanted) {
		if inaccessible {
			return nil, fmt.Errorf("port %d: %w", port, ErrProcessInaccessible)
		}
		return nil, fmt.Errorf("port %d: %w (owner hidden or exited)", port, ErrProcessInaccessible)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].PID < found[j].PID })
	return found, nil
}

// KillProcessOnPort preserves the single-owner confirmation contract for callers
// that display just one PID. Multi-owner callers must confirm the entire set.
func KillProcessOnPort(port int, expected *ProcessInfo, timeout time.Duration) error {
	return KillProcessesOnPort(port, []*ProcessInfo{expected}, timeout)
}

// KillProcessesOnPort validates the complete snapshot before sending signals.
// It never kills a new listener that appeared after inspection/confirmation.
func KillProcessesOnPort(port int, expected []*ProcessInfo, timeout time.Duration) error {
	current, err := FindProcessesByPort(port)
	if err != nil {
		return err
	}
	if len(current) != len(expected) {
		return fmt.Errorf("listener owners changed on port %d; confirmation expired", port)
	}
	confirmed := make(map[int]string, len(expected))
	for _, info := range expected {
		if info == nil || info.identity == "" {
			return fmt.Errorf("unconfirmed owner on port %d", port)
		}
		confirmed[info.PID] = info.identity
	}
	if len(confirmed) != len(current) {
		return fmt.Errorf("unconfirmed owners on port %d", port)
	}
	for _, info := range current {
		if confirmed[info.PID] != info.identity {
			return fmt.Errorf("listener owner changed on port %d; confirmation expired", port)
		}
	}
	var failures []error
	for _, info := range current {
		if err := killProcessByPID(info.PID, info.identity, timeout, false); err != nil {
			failures = append(failures, fmt.Errorf("PID %d on port %d: %w", info.PID, port, err))
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	for {
		if NewPortResolver("").IsPortAvailable(port) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("port %d remains occupied after termination", port)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// KillProcessByPID sends SIGTERM, waits, then escalates to SIGKILL if necessary.
// Success requires both process exit and successful binds on its former listeners.
func KillProcessByPID(pid int, timeout time.Duration) error {
	return killProcessByPID(pid, "", timeout, true)
}

func killProcessByPID(pid int, expectedIdentity string, timeout time.Duration, verifyBinds bool) error {
	if pid <= 1 || pid == os.Getpid() {
		return fmt.Errorf("refusing unsafe PID %d", pid)
	}
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	identity, _, err := processIdentity(pid)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if expectedIdentity != "" && identity != expectedIdentity {
		return fmt.Errorf("PID %d changed identity", pid)
	}
	name, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return err
	}
	cgroup, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return err
	}
	owner := strings.ToLower(string(name) + string(cgroup))
	for _, marker := range []string{"docker", "containerd", "kubepods", "libpod", "rootlesskit"} {
		if strings.Contains(owner, marker) {
			return fmt.Errorf("refusing to terminate container-related PID %d without container ownership", pid)
		}
	}
	inodes, err := socketInodes(pid)
	if err != nil {
		return err
	}
	listeners, err := tcpListeners()
	if err != nil {
		return err
	}
	var owned []tcpListener
	for _, l := range listeners {
		if inodes[l.inode] {
			owned = append(owned, l)
		}
	}
	exited := func() bool {
		current, zombie, err := processIdentity(pid)
		return errors.Is(err, os.ErrNotExist) || (err == nil && (zombie || current != identity))
	}
	signal := func(sig syscall.Signal) error {
		if exited() {
			return nil
		}
		current, _, err := processIdentity(pid)
		if err != nil {
			return err
		}
		if current != identity {
			return fmt.Errorf("PID %d changed identity", pid)
		}
		err = syscall.Kill(pid, sig)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	wait := func() bool {
		deadline := time.Now().Add(timeout)
		for {
			if exited() {
				return true
			}
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err := signal(syscall.SIGTERM); err != nil {
		return err
	}
	if !wait() {
		if err := signal(syscall.SIGKILL); err != nil {
			return err
		}
		if !wait() {
			return fmt.Errorf("PID %d did not exit after SIGKILL", pid)
		}
	}
	if !verifyBinds {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		free := true
		for _, l := range owned {
			listener, err := net.Listen(l.network, l.address)
			if err != nil {
				free = false
				break
			}
			listener.Close()
		}
		if free {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("TCP listeners remain occupied after PID %d exited", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
