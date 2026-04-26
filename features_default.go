//go:build !linux
// +build !linux

/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package conn

import "github.com/asciimoth/gonnect"

func supportsUDPOffload(_ gonnect.UDPConn) (txOffload, rxOffload bool) {
	return
}
