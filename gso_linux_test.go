//go:build linux

/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package conn

import (
	"encoding/binary"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func udpControlMessage(typ int32, value uint16) []byte {
	control := make([]byte, unix.CmsgSpace(sizeOfGSOData))
	hdr := (*unix.Cmsghdr)(unsafe.Pointer(&control[0]))
	hdr.Level = unix.SOL_UDP
	hdr.Type = typ
	hdr.SetLen(unix.CmsgLen(sizeOfGSOData))
	binary.LittleEndian.PutUint16(control[unix.CmsgLen(0):], value)
	return control
}

func Test_setGSOSizeAppendsWithoutOverwritingExistingControl(t *testing.T) {
	control := udpControlMessage(unix.UDP_GRO, 11)
	orig := append([]byte(nil), control...)
	control = append(control[:len(control):len(control)], make([]byte, gsoControlSize)...)
	control = control[:len(orig)]

	setGSOSize(&control, 33)

	if len(control) != len(orig)+gsoControlSize {
		t.Fatalf("len(control) = %d, want %d", len(control), len(orig)+gsoControlSize)
	}
	if string(control[:len(orig)]) != string(orig) {
		t.Fatalf("existing control prefix changed: got %v want %v", control[:len(orig)], orig)
	}

	rem := control[len(orig):]
	hdr, data, _, err := unix.ParseOneSocketControlMessage(rem)
	if err != nil {
		t.Fatalf("ParseOneSocketControlMessage err: %v", err)
	}
	if hdr.Level != unix.SOL_UDP {
		t.Fatalf("hdr.Level = %d, want %d", hdr.Level, unix.SOL_UDP)
	}
	if hdr.Type != unix.UDP_SEGMENT {
		t.Fatalf("hdr.Type = %d, want %d", hdr.Type, unix.UDP_SEGMENT)
	}
	if got := binary.LittleEndian.Uint16(data[:sizeOfGSOData]); got != 33 {
		t.Fatalf("gso data = %d, want 33", got)
	}
}

func Test_setGSOSizeShortCapacityLeavesControlUntouched(t *testing.T) {
	control := make([]byte, 1, gsoControlSize-1)
	orig := append([]byte(nil), control...)

	setGSOSize(&control, 55)

	if string(control) != string(orig) {
		t.Fatalf("control changed: got %v want %v", control, orig)
	}
}

func Test_getGSOSizeFindsUDPGROAmongOtherControlMessages(t *testing.T) {
	control := append(udpControlMessage(unix.UDP_SEGMENT, 7), udpControlMessage(unix.UDP_GRO, 29)...)

	got, err := getGSOSize(control)
	if err != nil {
		t.Fatalf("getGSOSize err: %v", err)
	}
	if got != 29 {
		t.Fatalf("getGSOSize = %d, want 29", got)
	}
}

func Test_getGSOSizeReportsParseError(t *testing.T) {
	control := make([]byte, unix.SizeofCmsghdr+1)
	hdr := (*unix.Cmsghdr)(unsafe.Pointer(&control[0]))
	hdr.SetLen(unix.CmsgLen(sizeOfGSOData))

	_, err := getGSOSize(control)
	if err == nil {
		t.Fatal("getGSOSize err = nil, want parse error")
	}
	if got, want := err.Error(), "error parsing socket control message"; len(got) < len(want) || got[:len(want)] != want {
		t.Fatalf("err = %q, want prefix %q", got, want)
	}
}
