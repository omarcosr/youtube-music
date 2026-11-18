package main

// One app per user, as requestSingleInstance in the TypeScript app (src/youtube-music.tsx), on the
// same named pipe and protocol (\\.\pipe\tarve-<sha256(id, user)>, one JSON launch line answered
// with the first instance's pid): a later launch, from Start, the taskbar, a media flyout or the
// installer's "open when done", brings the running app back instead of starting a second player.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procCreateNamedPipe     = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe    = kernel32.NewProc("ConnectNamedPipe")
	procDisconnectNamedPipe = kernel32.NewProc("DisconnectNamedPipe")
	procFlushFileBuffers    = kernel32.NewProc("FlushFileBuffers")
	procAllowSetForeground  = syscall.NewLazyDLL("user32.dll").NewProc("AllowSetForegroundWindow")
	errorPipeConnected      = syscall.Errno(535)
	errorPipeBusy           = syscall.Errno(231)
	firstPipeInstance       = uint32(0x00080000)
	pipeAccessDuplex        = uint32(3)
	pipeRejectRemoteClients = uint32(8)
	pipeUnlimitedInstances  = uint32(255)
)

// singleInstance is the first instance's pipe; release stops answering, so a restarted version
// (an update) becomes the first instance once this process is gone.
type singleInstance struct{ released atomic.Bool }

func (s *singleInstance) release() { s.released.Store(true) }

func instancePipe(id string) string {
	name := os.Getenv("USERNAME")
	if u, err := user.Current(); err == nil {
		name = u.Username[strings.LastIndex(u.Username, "\\")+1:]
	}
	sum := sha256.Sum256([]byte(id + "\x00" + name))
	return `\\.\pipe\tarve-` + hex.EncodeToString(sum[:])[:24]
}

// requestSingleInstance returns the first instance's handle, or nil when another instance runs
// (it was told about this launch and may take the foreground).
func requestSingleInstance(id string, onSecond func()) *singleInstance {
	pipe := instancePipe(id)
	deadline := time.Now().Add(10 * time.Second)
	for attempt := 0; attempt < 2 || time.Now().Before(deadline); attempt++ {
		if attempt > 1 {
			time.Sleep(min(50*time.Millisecond<<(attempt-2), 800*time.Millisecond))
		}
		if pid, ok := forwardLaunch(pipe); ok {
			if pid > 0 {
				procAllowSetForeground.Call(uintptr(pid))
			}
			return nil
		}
		if handle, err := createPipe(pipe, true); err == nil {
			s := &singleInstance{}
			go s.serve(pipe, handle, onSecond)
			return s
		}
	}
	// Neither reachable nor free: run rather than not start at all.
	return &singleInstance{}
}

func createPipe(pipe string, first bool) (syscall.Handle, error) {
	name, err := syscall.UTF16PtrFromString(pipe)
	if err != nil {
		return 0, err
	}
	mode := pipeAccessDuplex
	if first {
		mode |= firstPipeInstance
	}
	r, _, callErr := procCreateNamedPipe.Call(uintptr(unsafe.Pointer(name)), uintptr(mode), uintptr(pipeRejectRemoteClients),
		uintptr(pipeUnlimitedInstances), 4096, 4096, 0, 0)
	if syscall.Handle(r) == syscall.InvalidHandle {
		return 0, callErr
	}
	return syscall.Handle(r), nil
}

func (s *singleInstance) serve(pipe string, handle syscall.Handle, onSecond func()) {
	for !s.released.Load() {
		r, _, err := procConnectNamedPipe.Call(uintptr(handle), 0)
		if r == 0 && !errors.Is(err, errorPipeConnected) {
			syscall.CloseHandle(handle)
			return
		}
		if s.released.Load() {
			break
		}
		line, ok := readLine(handle)
		if ok {
			reply := []byte(strconv.Itoa(os.Getpid()) + "\n")
			var n uint32
			syscall.WriteFile(handle, reply, &n, nil)
			procFlushFileBuffers.Call(uintptr(handle))
			var launch struct{ Args []string }
			if json.Unmarshal(line, &launch) == nil {
				onSecond()
			}
		}
		procDisconnectNamedPipe.Call(uintptr(handle))
	}
	syscall.CloseHandle(handle)
}

func readLine(handle syscall.Handle) ([]byte, bool) {
	var data []byte
	buf := make([]byte, 4096)
	for len(data) < 1<<20 {
		var n uint32
		if err := syscall.ReadFile(handle, buf, &n, nil); err != nil || n == 0 {
			return nil, false
		}
		data = append(data, buf[:n]...)
		if i := strings.IndexByte(string(data), '\n'); i >= 0 {
			return data[:i], true
		}
	}
	return nil, false
}

// forwardLaunch hands this launch to a running instance; ok when one answered with its pid.
func forwardLaunch(pipe string) (int, bool) {
	name, err := syscall.UTF16PtrFromString(pipe)
	if err != nil {
		return 0, false
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, 0, 0)
	if errors.Is(err, errorPipeBusy) {
		time.Sleep(100 * time.Millisecond)
		handle, err = syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, 0, 0)
	}
	if err != nil {
		return 0, false
	}
	answered := make(chan int, 1)
	go func() {
		cwd, _ := os.Getwd()
		launch, _ := json.Marshal(map[string]any{"args": append([]string{}, os.Args[1:]...), "cwd": cwd, "urls": []string{}})
		var n uint32
		if syscall.WriteFile(handle, append(launch, '\n'), &n, nil) != nil {
			answered <- -1
			return
		}
		line, ok := readLine(handle)
		if !ok {
			answered <- -1
			return
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(line)))
		answered <- pid
	}()
	defer syscall.CloseHandle(handle)
	select {
	case pid := <-answered:
		return pid, pid >= 0
	case <-time.After(2 * time.Second):
		return 0, false
	}
}
