/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

// Package conn implements WireGuard's network connections.
package conn

import (
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"runtime"
	"strings"
)

const (
	IdealBatchSize = 128 // maximum number of packets handled per read and write
)

// A ReceiveFunc receives at least one packet from the network and writes them
// into packets. On a successful read it returns the number of elements of
// sizes, packets, and endpoints that should be evaluated. Some elements of
// sizes may be zero, and callers should ignore them. Callers must pass sizes
// and eps slices with a length greater than or equal to the length of packets.
// These lengths must not exceed the length of the associated Bind.BatchSize().
//
// ReceiveFuncs returned by Bind.Open remain valid until the next Bind.Close.
// They are expected to block waiting for input, may be called concurrently with
// Bind.Send, and must unblock and return net.ErrClosed after Close.
type ReceiveFunc func(packets [][]byte, sizes []int, eps []Endpoint) (n int, err error)

// A Bind listens on a port for both IPv6 and IPv4 UDP traffic.
//
// A Bind interface may also be a PeekLookAtSocketFd or BindSocketToInterface,
// depending on the platform-specific implementation.
type Bind interface {
	// Open puts the Bind into a listening state on a given port and reports the
	// actual port that it bound to. Passing zero results in a random selection.
	// fns is the set of functions that will be called to receive packets.
	//
	// Open fails with ErrBindAlreadyOpen if the bind is already open. Callers
	// should treat Open as a lifecycle transition rather than an operation to run
	// concurrently with another Open or Close.
	Open(port uint16) (fns []ReceiveFunc, actualPort uint16, err error)

	// Close closes the Bind listener.
	//
	// Close is safe to race with Send and with ReceiveFuncs returned by Open.
	// All ReceiveFuncs returned by Open must eventually return net.ErrClosed
	// after Close. Repeated Close calls are allowed.
	Close() error

	// SetMark sets the mark for each packet sent through this Bind.
	//
	// On Linux and Android this is SO_MARK; on FreeBSD and OpenBSD the platform
	// equivalent is used; on other platforms this is a no-op. Implementations
	// apply the mark to the currently open sockets, so it should be called after
	// Open and again after reopening a Bind.
	SetMark(mark uint32) error

	// Send writes one or more packets in bufs to address ep. The length of
	// bufs must not exceed BatchSize().
	//
	// Send is safe for concurrent use with other Send calls and with Close.
	// The endpoint must have been created by this Bind implementation's
	// ParseEndpoint, or otherwise be of the implementation-specific endpoint
	// type expected by the Bind. On Linux and Android, StdNetBind may coalesce
	// multiple datagrams into one sendmsg when UDP segmentation offload is
	// available; if the kernel rejects UDP GSO, it is disabled for the socket
	// and the send is retried without offload.
	Send(bufs [][]byte, ep Endpoint) error

	// ParseEndpoint creates a new endpoint from a string.
	//
	// Parsed endpoints are Bind-implementation specific. Endpoint values should
	// be treated as immutable once shared with Send or a ReceiveFunc; mutating an
	// endpoint's cached source state is not required to be thread-safe.
	ParseEndpoint(s string) (Endpoint, error)

	// BatchSize is the number of buffers expected to be passed to
	// the ReceiveFuncs, and the maximum expected to be passed to Send.
	//
	// StdNetBind returns IdealBatchSize on Linux and Android and 1 elsewhere.
	// WinRingBind currently returns 1.
	BatchSize() int
}

// BindSocketToInterface is implemented by Bind objects that support being
// tied to a single network interface. Used by wireguard-windows.
//
// These methods act on already-open sockets. They may also enable blackhole
// mode for the selected address family, causing subsequent sends for that
// family to be dropped locally.
type BindSocketToInterface interface {
	BindSocketToInterface4(interfaceIndex uint32, blackhole bool) error
	BindSocketToInterface6(interfaceIndex uint32, blackhole bool) error
}

// PeekLookAtSocketFd is implemented by Bind objects that support having their
// file descriptor peeked at. Used by wireguard-android.
//
// The returned fd is borrowed from the Bind; callers must not close it and
// should assume it becomes invalid after Close.
type PeekLookAtSocketFd interface {
	PeekLookAtSocketFd4() (fd int, err error)
	PeekLookAtSocketFd6() (fd int, err error)
}

// An Endpoint maintains the source/destination caching for a peer.
//
//	dst: the remote address of a peer ("endpoint" in uapi terminology)
//	src: the local address from which datagrams originate going to the peer
type Endpoint interface {
	ClearSrc()           // clears the source address
	SrcToString() string // returns the local source address (ip:port)
	DstToString() string // returns the destination address (ip:port)
	DstToBytes() []byte  // used for mac2 cookie calculations
	DstIP() netip.Addr
	SrcIP() netip.Addr
}

var (
	ErrBindAlreadyOpen   = errors.New("bind is already open")
	ErrNoNetwork         = errors.New("bind network is nil")
	ErrWrongEndpointType = errors.New("endpoint type does not correspond with bind type")
)

func (fn ReceiveFunc) PrettyName() string {
	name := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	// 0. cheese/taco.beansIPv6.func12.func21218-fm
	name = strings.TrimSuffix(name, "-fm")
	// 1. cheese/taco.beansIPv6.func12.func21218
	if idx := strings.LastIndexByte(name, '/'); idx != -1 {
		name = name[idx+1:]
		// 2. taco.beansIPv6.func12.func21218
	}
	for {
		var idx int
		for idx = len(name) - 1; idx >= 0; idx-- {
			if name[idx] < '0' || name[idx] > '9' {
				break
			}
		}
		if idx == len(name)-1 {
			break
		}
		const dotFunc = ".func"
		if !strings.HasSuffix(name[:idx+1], dotFunc) {
			break
		}
		name = name[:idx+1-len(dotFunc)]
		// 3. taco.beansIPv6.func12
		// 4. taco.beansIPv6
	}
	if idx := strings.LastIndexByte(name, '.'); idx != -1 {
		name = name[idx+1:]
		// 5. beansIPv6
	}
	if name == "" {
		return fmt.Sprintf("%p", fn)
	}
	if strings.HasSuffix(name, "IPv4") {
		return "v4"
	}
	if strings.HasSuffix(name, "IPv6") {
		return "v6"
	}
	return name
}
