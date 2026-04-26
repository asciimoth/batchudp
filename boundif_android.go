/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package conn

import "github.com/asciimoth/gonnect/sockopt"

func (s *StdNetBind) PeekLookAtSocketFd4() (fd int, err error) {
	fd, err = sockopt.GetFd(s.ipv4)
	if err != nil {
		return -1, err
	}
	return
}

func (s *StdNetBind) PeekLookAtSocketFd6() (fd int, err error) {
	fd, err = sockopt.GetFd(s.ipv6)
	if err != nil {
		return -1, err
	}
	return
}
