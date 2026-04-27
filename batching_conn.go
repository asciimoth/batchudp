/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package conn

import (
	"errors"
	"net/netip"

	"github.com/asciimoth/gonnect"
	"golang.org/x/net/ipv6"
)

// A BatchingConn is a gonnect UDP connection upgraded with batched I/O.
//
// Callers should prefer ReadBatch over single-packet read methods. A batching
// connection may receive GRO-coalesced datagrams that cannot be represented by
// ReadFromUDP or ReadFromUDPAddrPort without losing segmentation context.
type BatchingConn interface {
	gonnect.UDPConn

	// ReadBatch reads one or more datagrams into msgs and returns the number of
	// message slots the caller should inspect. Callers should size each
	// msgs[i].OOB slice capacity to at least MinControlMessageSize().
	ReadBatch(msgs []ipv6.Message, flags int) (n int, err error)

	// WriteBatchTo writes one or more datagrams to addr. The length of bufs must
	// not exceed BatchSize().
	WriteBatchTo(bufs [][]byte, addr netip.AddrPort) error

	// BatchSize reports the maximum number of datagrams expected by ReadBatch
	// and WriteBatchTo for this connection.
	BatchSize() int
}

var (
	ErrSinglePacketReadUnsupported = errors.New("single-packet reads are unsupported on batching connections")
	ErrBatchTooLarge               = errors.New("batch size exceeds batching connection capacity")
)
