//go:build windows

package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---- ICMP on Windows: iphlpapi!IcmpSendEcho2 ----
//
// Windows has no unprivileged ICMP socket. The two ways in are a raw socket, which
// needs Administrator, or iphlpapi's ICMP helper, which any user may call. We take
// the helper: it is what lets the service run as LocalService instead of
// LocalSystem, and what lets the portable build work from a double-click without a
// UAC prompt. See docs/adr/0002-windows-icmp.md.
//
// Two consequences fall out of that choice, and both shape this file:
//
//  1. ICMP_ECHO_REPLY.RoundTripTime is a ULONG of MILLISECONDS. For a tool whose
//     entire premise is the shape of the RTT distribution, 1ms buckets are useless —
//     a healthy LAN would read as a flat line of 0s and 1s. So we ignore that field
//     entirely and time the call ourselves. Go's time.Now() is backed by
//     QueryPerformanceCounter on Windows, giving sub-microsecond resolution; the
//     cost is that our number includes the syscall round trip through the helper,
//     a fixed overhead rather than a source of jitter. `pingping selftest`
//     measures both so the number is known rather than assumed.
//
//  2. The synchronous form blocks the calling thread for the full timeout when a
//     packet is lost. Sending a 20-packet round one at a time would take 20s of
//     wall clock against a black hole, overrunning even the slow pace. So packets
//     go out from separate goroutines, staggered by `gap` to reproduce the Unix
//     send pattern, with concurrency capped (see inFlight) to bound how many OS
//     threads a blocked round can park.

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho2   = iphlpapi.NewProc("IcmpSendEcho2")
)

const (
	invalidHandle = ^uintptr(0)
	ipSuccess     = 0

	// inFlight caps concurrent blocking calls per round. With the default 50ms gap
	// this never throttles a link whose RTT is under 400ms, so healthy targets are
	// paced exactly as on Unix; a fully black-holed round degrades to
	// ceil(packets/inFlight) * timeout instead of packets * timeout.
	inFlight = 8

	// replyBufSize holds one ICMP_ECHO_REPLY plus the echoed payload plus the 8
	// bytes an ICMP error message may carry. 1500 is far more than needed and
	// costs nothing — it is a per-call stack-lifetime allocation.
	replyBufSize = 1500
)

// ipOptionInformation mirrors the Win32 IP_OPTION_INFORMATION. Go's natural field
// alignment reproduces the C layout on both 386 (20 bytes) and amd64/arm64 (40
// bytes) for the reply struct below, so no manual padding is needed.
type ipOptionInformation struct {
	TTL         uint8
	Tos         uint8
	Flags       uint8
	OptionsSize uint8
	OptionsData uintptr
}

// icmpEchoReply mirrors the Win32 ICMP_ECHO_REPLY.
type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32 // milliseconds — deliberately unused, see the note above
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Options       ipOptionInformation
}

func icmpRound(host string, packets int, gap, timeout time.Duration) (Round, error) {
	r := Round{T: time.Now().Unix(), S: packets}
	ip, err := resolveIPv4(host)
	if err != nil {
		return r, err
	}
	// IPAddr holds the address in network byte order; on a little-endian host that
	// is the four octets read back as a little-endian uint32.
	dst := binary.LittleEndian.Uint32(ip[:])

	var nonce [4]byte
	rand.Read(nonce[:])
	payload := icmpPayload(nonce)

	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		sem  = make(chan struct{}, inFlight)
		open bool // at least one handle was obtained
	)
	for i := 0; i < packets; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if d := time.Duration(i) * gap; d > 0 {
				time.Sleep(d)
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			ms, ok, handled := icmpOnce(dst, payload, timeout)
			mu.Lock()
			if handled {
				open = true
			}
			if ok {
				r.R++
				r.MS = append(r.MS, ms)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	// A round in which not one handle could be opened is a local failure, not a
	// network result; reporting it as 100% loss would draw a black hole that isn't
	// there. Every other outcome — including every packet timing out — is real data.
	if !open {
		return r, fmt.Errorf("IcmpCreateFile failed for every packet (iphlpapi unavailable?)")
	}
	return r, nil
}

// icmpOnce sends a single echo request and times it. `handled` reports whether the
// ICMP handle itself could be opened, separating "the host cannot probe" from
// "the target did not answer".
func icmpOnce(dst uint32, payload []byte, timeout time.Duration) (ms float64, ok bool, handled bool) {
	h, _, _ := procIcmpCreateFile.Call()
	if h == invalidHandle || h == 0 {
		return 0, false, false
	}
	defer procIcmpCloseHandle.Call(h)

	reply := make([]byte, replyBufSize)
	to := timeout.Milliseconds()
	if to < 1 {
		to = 1
	}

	t0 := time.Now()
	n, _, _ := procIcmpSendEcho2.Call(
		h,
		0, // Event
		0, // ApcRoutine
		0, // ApcContext
		uintptr(dst),
		uintptr(unsafe.Pointer(&payload[0])),
		uintptr(len(payload)),
		0, // RequestOptions: default TTL/TOS
		uintptr(unsafe.Pointer(&reply[0])),
		uintptr(len(reply)),
		uintptr(to),
	)
	elapsed := time.Since(t0)
	// Keep both buffers alive across the call: the helper writes into them and Go
	// has no other reference once Call returns its uintptrs.
	runtime.KeepAlive(payload)
	runtime.KeepAlive(reply)

	if n == 0 {
		return 0, false, true // timed out, unreachable, or refused — all real loss
	}
	er := (*icmpEchoReply)(unsafe.Pointer(&reply[0]))
	if er.Status != ipSuccess {
		return 0, false, true
	}
	return float64(elapsed.Microseconds()) / 1000.0, true, true
}
