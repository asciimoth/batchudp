/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package conn

import (
	"github.com/asciimoth/gonnect"
	"github.com/asciimoth/gonnect/sockopt"
	"golang.org/x/sys/unix"
)

func supportsUDPOffload(conn gonnect.UDPConn) (txOffload, rxOffload bool) {
	err := sockopt.Control(conn, func(fd uintptr) {
		_, errSyscall := unix.GetsockoptInt(int(fd), unix.IPPROTO_UDP, unix.UDP_SEGMENT)
		txOffload = errSyscall == nil
		opt, errSyscall := unix.GetsockoptInt(int(fd), unix.IPPROTO_UDP, unix.UDP_GRO)
		rxOffload = errSyscall == nil && opt == 1
	})
	if err != nil {
		return false, false
	}
	return txOffload, rxOffload
}
