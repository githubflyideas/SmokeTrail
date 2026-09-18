package main

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"time"
)

// Round 是 SmokeTrail 的原子数据单元:一轮探测的全部原始样本。
// 存分布而不是均值 —— 这是整个项目的立项理由。
type Round struct {
	T  int64     `json:"t"`           // unix 秒
	S  int       `json:"s"`           // 发出
	R  int       `json:"r"`           // 收到
	MS []float64 `json:"ms"`          // RTT 样本(毫秒)
	B  bool      `json:"b,omitempty"` // flagged as a loss burst
	Z  float64   `json:"z,omitempty"` // robust z-score behind the flag (tooltip evidence)
}

// icmpPayload is the 14 data bytes every echo request carries: a magic string plus
// a per-round nonce. On Unix it is how a raw socket tells our replies from every
// other ping on the box; on Windows the kernel matches replies to handles for us,
// but the payload stays byte-identical so an RTT measured on either platform is
// measuring the same 22-byte ICMP message.
const icmpMagic = "smoketrail"

func icmpPayload(nonce [4]byte) []byte {
	p := make([]byte, 0, len(icmpMagic)+4)
	p = append(p, icmpMagic...)
	return append(p, nonce[:]...)
}

// probeParams 解析目标的探测节奏。优先级:显式 interval_sec > pace 档位 > 全局默认。
// fast 档同时把每轮包数提到 30,分布更细 —— 快节奏链路值得更好的分辨率。
func probeParams(t TargetCfg, p ProbeCfg) (interval time.Duration, packets int) {
	iv, pk := p.IntervalSec, p.Packets
	switch t.Pace {
	case "fast":
		iv, pk = 15, 30
	case "slow":
		iv = 300
	}
	if t.IntervalSec > 0 {
		iv = t.IntervalSec
	}
	return time.Duration(iv) * time.Second, pk
}

func probeLoop(t TargetCfg, p ProbeCfg, store *Store, det *Detector, stop chan struct{}) {
	interval, packets := probeParams(t, p)
	gap := time.Duration(p.GapMs) * time.Millisecond
	timeout := time.Duration(p.TimeoutMs) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		var r Round
		var err error
		switch t.Type {
		case "icmp":
			r, err = icmpRound(t.Host, packets, gap, timeout)
		case "tcp":
			r, err = tcpRound(t.Host, t.Port, packets, gap, timeout)
		}
		if err != nil {
			log.Printf("[%s] probe error: %v", t.Name, err)
			r = Round{T: time.Now().Unix(), S: packets, R: 0} // 解析失败按全丢记录,不留时间空洞
		}
		r.B, r.Z = det.CheckBurst(t.Name, r) // verdict + z evidence, written with the round
		if err := store.Append(t.Name, r); err != nil {
			log.Printf("[%s] write error: %v", t.Name, err)
		}

		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

func resolveIPv4(host string) ([4]byte, error) {
	var out [4]byte
	ips, err := net.LookupIP(host)
	if err != nil {
		return out, err
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			copy(out[:], v4)
			return out, nil
		}
	}
	return out, fmt.Errorf("%s 没有 IPv4 地址(v1 仅支持 IPv4)", host)
}

// ---- TCP:connect 耗时即 RTT 近似 ----
// Resolve once per round and dial the IP: Go doesn't cache DNS, so dialing a
// hostname would add a lookup to every sample and measure the resolver, not the link.
//
// Windows note: this is a full three-way handshake, not a half-open SYN probe.
// Raw TCP sends have been blocked since XP SP2, so a SYN-only probe would need
// Npcap — a driver install that would end the single-binary story. The connect
// timing is therefore what both platforms measure, and it includes the remote
// stack's accept path, not just the link.

func tcpRound(host string, port, packets int, gap, timeout time.Duration) (Round, error) {
	r := Round{T: time.Now().Unix(), S: packets}
	ips, err := net.LookupIP(host)
	if err != nil {
		return r, err
	}
	if len(ips) == 0 {
		return r, fmt.Errorf("%s: no address", host)
	}
	addr := net.JoinHostPort(ips[0].String(), strconv.Itoa(port))
	for i := 0; i < packets; i++ {
		t0 := time.Now()
		c, err := net.DialTimeout("tcp", addr, timeout)
		if err == nil {
			r.R++
			r.MS = append(r.MS, float64(time.Since(t0).Microseconds())/1000.0)
			c.Close()
		}
		if i < packets-1 {
			time.Sleep(gap)
		}
	}
	return r, nil
}
