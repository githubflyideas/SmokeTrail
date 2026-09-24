package main

import (
	"bytes"
	"strings"
	"testing"
)

// The probe payload goes on the wire, so its size is part of what this program
// measures. It was len(icmpMagic)+4 until a rename shortened the magic and took
// the ICMP message from 22 bytes to 20 with it — a change to every measurement,
// made by a search-and-replace, caught by nothing that runs on Linux.
//
// These run on every platform. The assertion that was supposed to catch it lived
// in icmp_windows_test.go, which only ever compiled on Windows, and it hardcoded
// the old number rather than deriving it.

func TestIcmpPayloadLengthIsPinned(t *testing.T) {
	if icmpPayloadLen != 12 {
		t.Fatalf("icmpPayloadLen is %d, was 12. Changing it changes what every "+
			"probe sends, so measurements either side of the change are of "+
			"different packets. If that is intended, update this number and say "+
			"so in the release notes.", icmpPayloadLen)
	}
	var nonce [4]byte
	if got := len(icmpPayload(nonce)); got != icmpPayloadLen {
		t.Fatalf("icmpPayload returned %d bytes, not icmpPayloadLen (%d)", got, icmpPayloadLen)
	}
}

// The length must not follow the magic — that coupling is the whole fault, and
// it is a property of how the constant is written, not of any value it produces.
func TestIcmpPayloadLengthIsALiteralNotDerived(t *testing.T) {
	src := readSource(t, "probe.go")
	for _, bad := range []string{
		"icmpPayloadLen = len(icmpMagic)",
		"icmpPayloadLen  = len(icmpMagic)",
	} {
		if strings.Contains(src, bad) {
			t.Fatalf("icmpPayloadLen is derived from the magic again (%q): renaming "+
				"the program would change every packet this sends", bad)
		}
	}
	if !strings.Contains(src, "icmpPayloadLen = 12") {
		t.Error("icmpPayloadLen is no longer a plain literal; keep it one so the " +
			"wire format cannot move by accident")
	}
}

// The nonce is how a raw socket on Unix tells our replies apart from every other
// ping on the box. It has to be findable, at a known place, and it has to differ
// per round or two rounds in flight cannot be told apart.
func TestIcmpPayloadCarriesMagicAndNonce(t *testing.T) {
	a := icmpPayload([4]byte{1, 2, 3, 4})
	b := icmpPayload([4]byte{9, 9, 9, 9})

	magic := icmpMagic
	if len(magic) > icmpPayloadLen-4 {
		magic = magic[:icmpPayloadLen-4]
	}
	if !bytes.HasPrefix(a, []byte(magic)) {
		t.Errorf("payload does not start with the magic: %q", a)
	}
	if got := a[icmpPayloadLen-4:]; !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Errorf("nonce is not in the last four bytes: %v", got)
	}
	if bytes.Equal(a, b) {
		t.Error("two different nonces produced identical payloads")
	}
	if !bytes.Equal(a[:icmpPayloadLen-4], b[:icmpPayloadLen-4]) {
		t.Error("the part before the nonce differs between rounds")
	}
}
