//go:build !windows

/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package conn

import "github.com/asciimoth/gonnect"

func NewDefaultBind(network gonnect.Network) Bind { return NewStdNetBind(network) }
