//go:build !linux

/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package conn

import "github.com/asciimoth/gonnect"

// TryUpgradeToBatchingConn is a no-op on non-Linux platforms.
func TryUpgradeToBatchingConn(pconn gonnect.PacketConn, _ string, _ int) gonnect.PacketConn {
	return pconn
}

// MinControlMessageSize reports the control buffer size required by ReadBatch.
func MinControlMessageSize() int {
	return 0
}
