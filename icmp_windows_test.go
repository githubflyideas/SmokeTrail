//go:build windows

package main

import (
	"testing"
	"unsafe"
)

// The reply structures in icmp_windows.go are hand-written mirrors of Win32 types.
// Nothing at compile time checks them against the real ABI, and getting a field
// offset wrong would not crash — it would silently read the wrong bytes and report
// plausible-looking nonsense, which is the worst failure mode a measurement tool
// has. These assertions come from the documented C layout:
//
//	typedef struct icmp_echo_reply {
//	  IPAddr Address; ULONG Status; ULONG RoundTripTime;
//	  USHORT DataSize; USHORT Reserved; PVOID Data;
//	  struct ip_option_information Options;
//	} ICMP_ECHO_REPLY;
//
// They run on every Windows CI job, on each architecture we ship.
func TestIcmpEchoReplyLayout(t *testing.T) {
	ptr := unsafe.Sizeof(uintptr(0))

	var r icmpEchoReply
	fields := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"Address", unsafe.Offsetof(r.Address), 0},
		{"Status", unsafe.Offsetof(r.Status), 4},
		{"RoundTripTime", unsafe.Offsetof(r.RoundTripTime), 8},
		{"DataSize", unsafe.Offsetof(r.DataSize), 12},
		{"Reserved", unsafe.Offsetof(r.Reserved), 14},
		// Data is a pointer, so it aligns to the pointer size: 16 either way.
		{"Data", unsafe.Offsetof(r.Data), 16},
		{"Options", unsafe.Offsetof(r.Options), 16 + ptr},
	}
	for _, f := range fields {
		if f.got != f.want {
			t.Errorf("ICMP_ECHO_REPLY.%s at offset %d, want %d", f.name, f.got, f.want)
		}
	}

	var o ipOptionInformation
	optFields := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"TTL", unsafe.Offsetof(o.TTL), 0},
		{"Tos", unsafe.Offsetof(o.Tos), 1},
		{"Flags", unsafe.Offsetof(o.Flags), 2},
		{"OptionsSize", unsafe.Offsetof(o.OptionsSize), 3},
		{"OptionsData", unsafe.Offsetof(o.OptionsData), ptr},
	}
	for _, f := range optFields {
		if f.got != f.want {
			t.Errorf("IP_OPTION_INFORMATION.%s at offset %d, want %d", f.name, f.got, f.want)
		}
	}

	// amd64/arm64: 40 bytes. 386: 28.
	wantSize := 16 + ptr + 2*ptr
	if got := unsafe.Sizeof(r); got != wantSize {
		t.Errorf("sizeof(ICMP_ECHO_REPLY) = %d, want %d", got, wantSize)
	}

	// MSDN: the reply buffer must hold one ICMP_ECHO_REPLY + RequestSize + 8.
	var nonce [4]byte
	if need := unsafe.Sizeof(r) + uintptr(len(icmpPayload(nonce))) + 8; replyBufSize < int(need) {
		t.Errorf("replyBufSize %d is below the documented minimum %d", replyBufSize, need)
	}
}

// The payload must stay byte-identical to the Unix one, or an RTT measured on
// Windows is measuring a different-sized packet than the same target on Linux.
func TestIcmpPayloadWireCompatible(t *testing.T) {
	var nonce [4]byte
	if got := len(icmpPayload(nonce)); got != 14 {
		t.Fatalf("payload is %d bytes, want 14 (8-byte ICMP header + 14 = 22 on the wire)", got)
	}
}

// IcmpCreateFile reports failure as INVALID_HANDLE_VALUE, which is all-ones, not 0.
func TestInvalidHandleSentinel(t *testing.T) {
	if invalidHandle != ^uintptr(0) {
		t.Fatal("invalidHandle must be INVALID_HANDLE_VALUE (all bits set)")
	}
}
