//go:build linux

/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package conn

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"

	"github.com/asciimoth/gonnect"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type xnetBatchReader interface {
	ReadBatch([]ipv6.Message, int) (int, error)
}

type xnetBatchWriter interface {
	WriteBatch([]ipv6.Message, int) (int, error)
}

type xnetBatchReadWriter interface {
	xnetBatchReader
	xnetBatchWriter
}

var _ BatchingConn = (*linuxBatchingConn)(nil)

type linuxBatchingConn struct {
	gonnect.UDPConn

	xpc       xnetBatchReadWriter
	rxOffload bool
	txOffload atomic.Bool
	batchSize int

	sendBatchPool sync.Pool
}

type batchingSendBatch struct {
	msgs []ipv6.Message
	ua   *net.UDPAddr
}

func (c *linuxBatchingConn) BatchSize() int {
	return c.batchSize
}

func (c *linuxBatchingConn) ReadFromUDP([]byte) (int, *net.UDPAddr, error) {
	return 0, nil, ErrSinglePacketReadUnsupported
}

func (c *linuxBatchingConn) ReadFromUDPAddrPort([]byte) (int, netip.AddrPort, error) {
	return 0, netip.AddrPort{}, ErrSinglePacketReadUnsupported
}

func (c *linuxBatchingConn) ReadBatch(msgs []ipv6.Message, flags int) (n int, err error) {
	if len(msgs) == 0 {
		return 0, nil
	}
	if !c.rxOffload || len(msgs) < 2 {
		for i := range msgs {
			msgs[i].OOB = msgs[i].OOB[:cap(msgs[i].OOB)]
		}
		return c.xpc.ReadBatch(msgs, flags)
	}

	readAt := len(msgs) - 2
	for i := readAt; i < len(msgs); i++ {
		msgs[i].OOB = msgs[i].OOB[:cap(msgs[i].OOB)]
	}
	n, err = c.xpc.ReadBatch(msgs[readAt:], flags)
	if err != nil || n == 0 {
		return 0, err
	}
	return splitCoalescedMessages(msgs, readAt, getGSOSize)
}

func (c *linuxBatchingConn) WriteBatchTo(bufs [][]byte, addr netip.AddrPort) error {
	if len(bufs) > c.batchSize {
		return ErrBatchTooLarge
	}
	if len(bufs) == 0 {
		return nil
	}

	batch := c.getSendBatch()
	defer c.putSendBatch(batch)

	if addr.Addr().Is6() {
		as16 := addr.Addr().As16()
		copy(batch.ua.IP, as16[:])
		batch.ua.IP = batch.ua.IP[:16]
	} else {
		as4 := addr.Addr().As4()
		copy(batch.ua.IP, as4[:])
		batch.ua.IP = batch.ua.IP[:4]
	}
	batch.ua.Port = int(addr.Port())

	var (
		n       int
		retried bool
		err     error
	)

retry:
	if c.txOffload.Load() {
		n = c.coalesceMessages(batch.ua, bufs, batch.msgs)
		err = c.writeBatch(batch.msgs[:n])
		if err != nil && c.txOffload.Load() && errShouldDisableUDPGSO(err) {
			c.txOffload.Store(false)
			retried = true
			goto retry
		}
	} else {
		for i := range bufs {
			batch.msgs[i].Buffers[0] = bufs[i]
			batch.msgs[i].Addr = batch.ua
			batch.msgs[i].OOB = batch.msgs[i].OOB[:0]
		}
		err = c.writeBatch(batch.msgs[:len(bufs)])
	}

	if retried {
		return ErrUDPGSODisabled{onLaddr: c.LocalAddr().String(), RetryErr: err}
	}
	return err
}

func (c *linuxBatchingConn) writeBatch(msgs []ipv6.Message) error {
	var start int
	for {
		n, err := c.xpc.WriteBatch(msgs[start:], 0)
		if err != nil || n == len(msgs[start:]) {
			return err
		}
		start += n
	}
}

func (c *linuxBatchingConn) getSendBatch() *batchingSendBatch {
	return c.sendBatchPool.Get().(*batchingSendBatch)
}

func (c *linuxBatchingConn) putSendBatch(batch *batchingSendBatch) {
	for i := range batch.msgs {
		batch.msgs[i] = ipv6.Message{
			Buffers: batch.msgs[i].Buffers[:1],
			OOB:     batch.msgs[i].OOB[:0],
		}
	}
	c.sendBatchPool.Put(batch)
}

func (c *linuxBatchingConn) coalesceMessages(addr *net.UDPAddr, bufs [][]byte, msgs []ipv6.Message) int {
	var (
		base     = -1
		gsoSize  int
		dgramCnt int
		endBatch bool
	)
	maxPayloadLen := maxIPv4PayloadLen
	if addr.IP.To4() == nil {
		maxPayloadLen = maxIPv6PayloadLen
	}
	for i, buf := range bufs {
		if i > 0 {
			msgLen := len(buf)
			baseLenBefore := 0
			for _, chunk := range msgs[base].Buffers {
				baseLenBefore += len(chunk)
			}
			if msgLen+baseLenBefore <= maxPayloadLen &&
				msgLen <= gsoSize &&
				dgramCnt < udpSegmentMaxDatagrams &&
				!endBatch {
				msgs[base].Buffers = append(msgs[base].Buffers, buf)
				if i == len(bufs)-1 {
					setGSOSize(&msgs[base].OOB, uint16(gsoSize))
				}
				dgramCnt++
				if msgLen < gsoSize {
					endBatch = true
				}
				continue
			}
		}
		if dgramCnt > 1 {
			setGSOSize(&msgs[base].OOB, uint16(gsoSize))
		}
		endBatch = false
		base++
		gsoSize = len(buf)
		msgs[base].OOB = msgs[base].OOB[:0]
		msgs[base].Buffers[0] = buf
		msgs[base].Addr = addr
		dgramCnt = 1
	}
	return base + 1
}

// TryUpgradeToBatchingConn upgrades a native-backed gonnect packet connection
// to a BatchingConn when Linux batching support is available.
func TryUpgradeToBatchingConn(pconn gonnect.PacketConn, network string, batchSize int) gonnect.PacketConn {
	if batchConn, ok := pconn.(BatchingConn); ok {
		return batchConn
	}
	if network != "udp4" && network != "udp6" {
		return pconn
	}
	udpConn, ok := pconn.(gonnect.UDPConn)
	if !ok {
		return pconn
	}
	nativeConn := unwrapUDPConn(udpConn)
	if nativeConn == nil {
		return pconn
	}
	if batchSize <= 0 {
		batchSize = IdealBatchSize
	}

	var xpc xnetBatchReadWriter
	switch network {
	case "udp4":
		xpc = ipv4.NewPacketConn(nativeConn)
	case "udp6":
		xpc = ipv6.NewPacketConn(nativeConn)
	default:
		return pconn
	}

	txOffload, rxOffload := supportsUDPOffload(udpConn)
	conn := &linuxBatchingConn{
		UDPConn:   udpConn,
		xpc:       xpc,
		rxOffload: rxOffload,
		batchSize: batchSize,
		sendBatchPool: sync.Pool{
			New: func() any {
				ua := &net.UDPAddr{IP: make([]byte, 16)}
				msgs := make([]ipv6.Message, batchSize)
				for i := range msgs {
					msgs[i].Buffers = make(net.Buffers, 1, udpSegmentMaxDatagrams)
					msgs[i].Addr = ua
					msgs[i].OOB = make([]byte, 0, gsoControlSize)
				}
				return &batchingSendBatch{msgs: msgs, ua: ua}
			},
		},
	}
	conn.txOffload.Store(txOffload)
	return conn
}

// MinControlMessageSize reports the control buffer size required by ReadBatch.
func MinControlMessageSize() int {
	return gsoControlSize
}
