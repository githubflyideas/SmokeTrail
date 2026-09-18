//go:build linux || darwin

package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"time"
)

// ---- ICMP:纯 syscall,无第三方依赖 ----
// 优先 SOCK_DGRAM(无需 root,需 sysctl ping_group_range 允许),
// 失败回退 SOCK_RAW(需要 root 或 cap_net_raw)。

func icmpRound(host string, packets int, gap, timeout time.Duration) (Round, error) {
	r := Round{T: time.Now().Unix(), S: packets}
	ip, err := resolveIPv4(host)
	if err != nil {
		return r, err
	}

	fd, raw, err := icmpSocket()
	if err != nil {
		return r, fmt.Errorf("cannot open an ICMP socket (needs net.ipv4.ping_group_range or cap_net_raw, see README): %w", err)
	}
	defer syscall.Close(fd)

	id := os.Getpid() & 0xffff
	var nonce [4]byte
	rand.Read(nonce[:])
	dst := &syscall.SockaddrInet4{Addr: ip}

	sendAt := make([]time.Time, packets)
	got := make([]bool, packets)

	for i := 0; i < packets; i++ {
		pkt := buildEcho(id, i, nonce)
		sendAt[i] = time.Now()
		if err := syscall.Sendto(fd, pkt, 0, dst); err != nil {
			continue // 单包发送失败不终止整轮
		}
		collect(fd, raw, id, nonce, sendAt, got, &r, time.Now().Add(gap))
	}
	// 尾窗:等最后一批在途包
	collect(fd, raw, id, nonce, sendAt, got, &r, time.Now().Add(timeout))
	return r, nil
}

func icmpSocket() (fd int, raw bool, err error) {
	fd, err = syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_ICMP)
	if err == nil {
		return fd, false, nil
	}
	fd, err = syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_ICMP)
	return fd, true, err
}

func buildEcho(id, seq int, nonce [4]byte) []byte {
	data := icmpPayload(nonce)
	p := make([]byte, 8+len(data))
	p[0] = 8 // echo request
	binary.BigEndian.PutUint16(p[4:6], uint16(id))
	binary.BigEndian.PutUint16(p[6:8], uint16(seq))
	copy(p[8:], data)
	cs := icmpChecksum(p)
	binary.BigEndian.PutUint16(p[2:4], cs)
	return p
}

// collect 在 deadline 前持续收包,匹配到未回收的 seq 就记 RTT。
// 乱序、迟到的回包都能被后续窗口捞回,RTT 按各自 sendAt 计算,不失真。
func collect(fd int, raw bool, id int, nonce [4]byte, sendAt []time.Time, got []bool, r *Round, deadline time.Time) {
	buf := make([]byte, 1500)
	want := icmpPayload(nonce)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			return
		}
		wait := remain
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond // 50ms 一跳,保证按时退出
		}
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		tv := syscall.NsecToTimeval(wait.Nanoseconds()) // 可移植:linux/darwin 的 Timeval 字段类型不同
		syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		now := time.Now()
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK || err == syscall.EINTR {
				continue
			}
			return
		}
		pkt := buf[:n]
		if raw && n >= 20 { // raw socket 带 IP 头,剥掉
			ihl := int(pkt[0]&0x0f) * 4
			if n <= ihl {
				continue
			}
			pkt = pkt[ihl:n]
		}
		if len(pkt) < 8+len(want) || pkt[0] != 0 { // 只认 echo reply
			continue
		}
		// raw socket 会收到本机所有 ICMP 回包,靠 id+nonce 隔离并发目标
		if raw && int(binary.BigEndian.Uint16(pkt[4:6])) != id {
			continue
		}
		if string(pkt[8:8+len(want)]) != string(want) {
			continue
		}
		seq := int(binary.BigEndian.Uint16(pkt[6:8]))
		if seq < 0 || seq >= len(sendAt) || got[seq] {
			continue
		}
		got[seq] = true
		r.R++
		r.MS = append(r.MS, float64(now.Sub(sendAt[seq]).Microseconds())/1000.0)
	}
}

func icmpChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 > 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}
