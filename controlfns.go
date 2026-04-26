/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package conn

import (
	"syscall"

	"github.com/asciimoth/gonnect"
	"github.com/asciimoth/gonnect/helpers"
)

// UDP socket read/write buffer size (7MB). The value of 7MB is chosen as it is
// the max supported by a default configuration of macOS. Some platforms will
// silently clamp the value to other maximums, such as linux clamping to
// net.core.{r,w}mem_max (see _linux.go for additional implementation that works
// around this limitation)
const socketBufferSize = 7 << 20

// controlFn is the callback function signature from net.ListenConfig.Control.
// It is used to apply platform specific configuration to the socket prior to
// bind.
type controlFn func(network, address string, c syscall.RawConn) error

// listenControlFns are applied before bind through gonnect.ListenConfig when
// the supplied Network supports it.
var listenControlFns = []controlFn{}

// socketOpenControlFns are applied after socket creation. Helpers should treat
// unsupported raw-socket access as a no-op so virtual and wrapped networks can
// still function.
var socketOpenControlFns = []controlFn{}

// listenConfig returns a gonnect.ListenConfig that applies listenControlFns to
// the socket prior to bind.
func listenConfig() *gonnect.ListenConfig {
	return &gonnect.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			for _, fn := range listenControlFns {
				if err := fn(network, address, c); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func configureSocket(conn gonnect.UDPConn, network, address string) error {
	rc, err := helpers.SyscallConn(conn)
	if err != nil || rc == nil {
		return err
	}
	for _, fn := range socketOpenControlFns {
		if err := fn(network, address, rc); err != nil {
			return err
		}
	}
	return nil
}
