//go:build linux && !android

/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package conn

import (
	"context"
	"net"
	"net/netip"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func setSrc(ep *StdNetEndpoint, addr netip.Addr, ifidx int32) {
	var buf []byte
	if addr.Is4() {
		buf = make([]byte, unix.CmsgSpace(unix.SizeofInet4Pktinfo))
		hdr := unix.Cmsghdr{
			Level: unix.IPPROTO_IP,
			Type:  unix.IP_PKTINFO,
		}
		hdr.SetLen(unix.CmsgLen(unix.SizeofInet4Pktinfo))
		copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(&hdr)), int(unsafe.Sizeof(hdr))))

		info := unix.Inet4Pktinfo{
			Ifindex:  ifidx,
			Spec_dst: addr.As4(),
		}
		copy(buf[unix.CmsgLen(0):], unsafe.Slice((*byte)(unsafe.Pointer(&info)), unix.SizeofInet4Pktinfo))
	} else {
		buf = make([]byte, unix.CmsgSpace(unix.SizeofInet6Pktinfo))
		hdr := unix.Cmsghdr{
			Level: unix.IPPROTO_IPV6,
			Type:  unix.IPV6_PKTINFO,
		}
		hdr.SetLen(unix.CmsgLen(unix.SizeofInet6Pktinfo))
		copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(&hdr)), int(unsafe.Sizeof(hdr))))

		info := unix.Inet6Pktinfo{
			Ifindex: uint32(ifidx),
			Addr:    addr.As16(),
		}
		copy(buf[unix.CmsgLen(0):], unsafe.Slice((*byte)(unsafe.Pointer(&info)), unix.SizeofInet6Pktinfo))
	}

	ep.src = buf
}

func Test_setSrcControl(t *testing.T) {
	t.Run("IPv4", func(t *testing.T) {
		ep := &StdNetEndpoint{
			AddrPort: netip.MustParseAddrPort("127.0.0.1:1234"),
		}
		setSrc(ep, netip.MustParseAddr("127.0.0.1"), 5)

		control := make([]byte, stickyControlSize)

		setSrcControl(&control, ep)

		hdr := (*unix.Cmsghdr)(unsafe.Pointer(&control[0]))
		if hdr.Level != unix.IPPROTO_IP {
			t.Errorf("unexpected level: %d", hdr.Level)
		}
		if hdr.Type != unix.IP_PKTINFO {
			t.Errorf("unexpected type: %d", hdr.Type)
		}
		if uint(hdr.Len) != uint(unix.CmsgLen(int(unsafe.Sizeof(unix.Inet4Pktinfo{})))) {
			t.Errorf("unexpected length: %d", hdr.Len)
		}
		info := (*unix.Inet4Pktinfo)(unsafe.Pointer(&control[unix.CmsgLen(0)]))
		if info.Spec_dst[0] != 127 || info.Spec_dst[1] != 0 || info.Spec_dst[2] != 0 || info.Spec_dst[3] != 1 {
			t.Errorf("unexpected address: %v", info.Spec_dst)
		}
		if info.Ifindex != 5 {
			t.Errorf("unexpected ifindex: %d", info.Ifindex)
		}
	})

	t.Run("IPv6", func(t *testing.T) {
		ep := &StdNetEndpoint{
			AddrPort: netip.MustParseAddrPort("[::1]:1234"),
		}
		setSrc(ep, netip.MustParseAddr("::1"), 5)

		control := make([]byte, stickyControlSize)

		setSrcControl(&control, ep)

		hdr := (*unix.Cmsghdr)(unsafe.Pointer(&control[0]))
		if hdr.Level != unix.IPPROTO_IPV6 {
			t.Errorf("unexpected level: %d", hdr.Level)
		}
		if hdr.Type != unix.IPV6_PKTINFO {
			t.Errorf("unexpected type: %d", hdr.Type)
		}
		if uint(hdr.Len) != uint(unix.CmsgLen(int(unsafe.Sizeof(unix.Inet6Pktinfo{})))) {
			t.Errorf("unexpected length: %d", hdr.Len)
		}
		info := (*unix.Inet6Pktinfo)(unsafe.Pointer(&control[unix.CmsgLen(0)]))
		if info.Addr != ep.SrcIP().As16() {
			t.Errorf("unexpected address: %v", info.Addr)
		}
		if info.Ifindex != 5 {
			t.Errorf("unexpected ifindex: %d", info.Ifindex)
		}
	})

	t.Run("ClearOnNoSrc", func(t *testing.T) {
		control := make([]byte, stickyControlSize)
		hdr := (*unix.Cmsghdr)(unsafe.Pointer(&control[0]))
		hdr.Level = 1
		hdr.Type = 2
		hdr.Len = 3

		setSrcControl(&control, &StdNetEndpoint{})

		if len(control) != 0 {
			t.Errorf("unexpected control: %v", control)
		}
	})

	t.Run("InsufficientCapacityLeavesControlUntouched", func(t *testing.T) {
		ep := &StdNetEndpoint{
			AddrPort: netip.MustParseAddrPort("127.0.0.1:1234"),
		}
		setSrc(ep, netip.MustParseAddr("127.0.0.1"), 5)

		control := []byte{1, 2, 3}
		want := append([]byte(nil), control...)

		setSrcControl(&control, ep)

		if string(control) != string(want) {
			t.Fatalf("control changed: got %v want %v", control, want)
		}
	})
}

func Test_getSrcFromControl(t *testing.T) {
	t.Run("IPv4", func(t *testing.T) {
		control := make([]byte, stickyControlSize)
		hdr := (*unix.Cmsghdr)(unsafe.Pointer(&control[0]))
		hdr.Level = unix.IPPROTO_IP
		hdr.Type = unix.IP_PKTINFO
		hdr.SetLen(unix.CmsgLen(int(unsafe.Sizeof(unix.Inet4Pktinfo{}))))
		info := (*unix.Inet4Pktinfo)(unsafe.Pointer(&control[unix.CmsgLen(0)]))
		info.Spec_dst = [4]byte{127, 0, 0, 1}
		info.Ifindex = 5

		ep := &StdNetEndpoint{}
		getSrcFromControl(control, ep)

		if ep.SrcIP() != netip.MustParseAddr("127.0.0.1") {
			t.Errorf("unexpected address: %v", ep.SrcIP())
		}
		if ep.SrcIfidx() != 5 {
			t.Errorf("unexpected ifindex: %d", ep.SrcIfidx())
		}
	})
	t.Run("IPv6", func(t *testing.T) {
		control := make([]byte, stickyControlSize)
		hdr := (*unix.Cmsghdr)(unsafe.Pointer(&control[0]))
		hdr.Level = unix.IPPROTO_IPV6
		hdr.Type = unix.IPV6_PKTINFO
		hdr.SetLen(unix.CmsgLen(int(unsafe.Sizeof(unix.Inet6Pktinfo{}))))
		info := (*unix.Inet6Pktinfo)(unsafe.Pointer(&control[unix.CmsgLen(0)]))
		info.Addr = [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		info.Ifindex = 5

		ep := &StdNetEndpoint{}
		getSrcFromControl(control, ep)

		if ep.SrcIP() != netip.MustParseAddr("::1") {
			t.Errorf("unexpected address: %v", ep.SrcIP())
		}
		if ep.SrcIfidx() != 5 {
			t.Errorf("unexpected ifindex: %d", ep.SrcIfidx())
		}
	})
	t.Run("ClearOnEmpty", func(t *testing.T) {
		var control []byte
		ep := &StdNetEndpoint{}
		setSrc(ep, netip.MustParseAddr("::1"), 5)

		getSrcFromControl(control, ep)
		if ep.SrcIP().IsValid() {
			t.Errorf("unexpected address: %v", ep.SrcIP())
		}
		if ep.SrcIfidx() != 0 {
			t.Errorf("unexpected ifindex: %d", ep.SrcIfidx())
		}
	})
	t.Run("Multiple", func(t *testing.T) {
		zeroControl := make([]byte, unix.CmsgSpace(0))
		zeroHdr := (*unix.Cmsghdr)(unsafe.Pointer(&zeroControl[0]))
		zeroHdr.SetLen(unix.CmsgLen(0))

		control := make([]byte, unix.CmsgSpace(unix.SizeofInet4Pktinfo))
		hdr := (*unix.Cmsghdr)(unsafe.Pointer(&control[0]))
		hdr.Level = unix.IPPROTO_IP
		hdr.Type = unix.IP_PKTINFO
		hdr.SetLen(unix.CmsgLen(int(unsafe.Sizeof(unix.Inet4Pktinfo{}))))
		info := (*unix.Inet4Pktinfo)(unsafe.Pointer(&control[unix.CmsgLen(0)]))
		info.Spec_dst = [4]byte{127, 0, 0, 1}
		info.Ifindex = 5

		combined := make([]byte, 0)
		combined = append(combined, zeroControl...)
		combined = append(combined, control...)

		ep := &StdNetEndpoint{}
		getSrcFromControl(combined, ep)

		if ep.SrcIP() != netip.MustParseAddr("127.0.0.1") {
			t.Errorf("unexpected address: %v", ep.SrcIP())
		}
		if ep.SrcIfidx() != 5 {
			t.Errorf("unexpected ifindex: %d", ep.SrcIfidx())
		}
	})
	t.Run("FirstPktinfoWins", func(t *testing.T) {
		first := make([]byte, unix.CmsgSpace(unix.SizeofInet4Pktinfo))
		firstHdr := (*unix.Cmsghdr)(unsafe.Pointer(&first[0]))
		firstHdr.Level = unix.IPPROTO_IP
		firstHdr.Type = unix.IP_PKTINFO
		firstHdr.SetLen(unix.CmsgLen(int(unsafe.Sizeof(unix.Inet4Pktinfo{}))))
		firstInfo := (*unix.Inet4Pktinfo)(unsafe.Pointer(&first[unix.CmsgLen(0)]))
		firstInfo.Spec_dst = [4]byte{127, 0, 0, 1}
		firstInfo.Ifindex = 5

		second := make([]byte, unix.CmsgSpace(unix.SizeofInet4Pktinfo))
		secondHdr := (*unix.Cmsghdr)(unsafe.Pointer(&second[0]))
		secondHdr.Level = unix.IPPROTO_IP
		secondHdr.Type = unix.IP_PKTINFO
		secondHdr.SetLen(unix.CmsgLen(int(unsafe.Sizeof(unix.Inet4Pktinfo{}))))
		secondInfo := (*unix.Inet4Pktinfo)(unsafe.Pointer(&second[unix.CmsgLen(0)]))
		secondInfo.Spec_dst = [4]byte{127, 0, 0, 2}
		secondInfo.Ifindex = 9

		ep := &StdNetEndpoint{}
		getSrcFromControl(append(first, second...), ep)

		if got := ep.SrcIP(); got != netip.MustParseAddr("127.0.0.1") {
			t.Fatalf("SrcIP = %v, want 127.0.0.1", got)
		}
		if got := ep.SrcIfidx(); got != 5 {
			t.Fatalf("SrcIfidx = %d, want 5", got)
		}
	})
}

func Test_listenConfig(t *testing.T) {
	t.Run("IPv4", func(t *testing.T) {
		conn, err := listenConfig().ListenPacket(context.Background(), "udp4", ":0")
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		sc, err := conn.(*net.UDPConn).SyscallConn()
		if err != nil {
			t.Fatal(err)
		}

		if runtime.GOOS == "linux" {
			var i int
			sc.Control(func(fd uintptr) {
				i, err = unix.GetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_PKTINFO)
			})
			if err != nil {
				t.Fatal(err)
			}
			if i != 1 {
				t.Error("IP_PKTINFO not set!")
			}
		} else {
			t.Logf("listenConfig() does not set IPV6_RECVPKTINFO on %s", runtime.GOOS)
		}
	})
	t.Run("IPv6", func(t *testing.T) {
		conn, err := listenConfig().ListenPacket(context.Background(), "udp6", ":0")
		if err != nil {
			t.Fatal(err)
		}
		sc, err := conn.(*net.UDPConn).SyscallConn()
		if err != nil {
			t.Fatal(err)
		}

		if runtime.GOOS == "linux" {
			var i int
			sc.Control(func(fd uintptr) {
				i, err = unix.GetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_RECVPKTINFO)
			})
			if err != nil {
				t.Fatal(err)
			}
			if i != 1 {
				t.Error("IPV6_PKTINFO not set!")
			}
		} else {
			t.Logf("listenConfig() does not set IPV6_RECVPKTINFO on %s", runtime.GOOS)
		}
	})
}
