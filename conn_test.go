/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package conn

import (
	"errors"
	"testing"
)

func TestPrettyName(t *testing.T) {
	var (
		recvFunc ReceiveFunc = func(bufs [][]byte, sizes []int, eps []Endpoint) (n int, err error) { return }
	)

	const want = "TestPrettyName"

	t.Run("ReceiveFunc.PrettyName", func(t *testing.T) {
		if got := recvFunc.PrettyName(); got != want {
			t.Errorf("PrettyName() = %v, want %v", got, want)
		}
	})
}

func TestValidateReceiveBuffers(t *testing.T) {
	tests := []struct {
		name    string
		packets int
		sizes   int
		eps     int
		batch   int
		wantErr error
	}{
		{name: "exact", packets: 2, sizes: 2, eps: 2, batch: 2},
		{name: "packet list short", packets: 1, sizes: 2, eps: 2, batch: 2, wantErr: ErrReadBufferTooShort},
		{name: "sizes short", packets: 2, sizes: 1, eps: 2, batch: 2, wantErr: ErrReadBufferTooShort},
		{name: "eps short", packets: 2, sizes: 2, eps: 1, batch: 2, wantErr: ErrReadBufferTooShort},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReceiveBuffers(
				make([][]byte, tt.packets),
				make([]int, tt.sizes),
				make([]Endpoint, tt.eps),
				tt.batch,
			)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateReceiveBuffers() err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
