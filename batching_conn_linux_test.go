//go:build linux

package conn

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/asciimoth/gonnect"
	"golang.org/x/net/ipv6"
)

func TestTryUpgradeToBatchingConnUpgradesNativeUDPConn(t *testing.T) {
	udpConn := listenUDP4(t)

	upgraded := TryUpgradeToBatchingConn(udpConn, "udp4", 7)
	bc, ok := upgraded.(BatchingConn)
	if !ok {
		t.Fatalf("TryUpgradeToBatchingConn() type = %T, want BatchingConn", upgraded)
	}
	if _, ok := upgraded.(gonnect.UDPConn); !ok {
		t.Fatalf("TryUpgradeToBatchingConn() type = %T, want gonnect.UDPConn", upgraded)
	}
	if got := bc.BatchSize(); got != 7 {
		t.Fatalf("BatchSize() = %d, want 7", got)
	}
}

func TestBatchingConnReadBatchAndWriteBatchTo(t *testing.T) {
	serverUDP := listenUDP4(t)
	clientUDP := listenUDP4(t)

	server := TryUpgradeToBatchingConn(serverUDP, "udp4", 2).(BatchingConn)
	client := TryUpgradeToBatchingConn(clientUDP, "udp4", 2).(BatchingConn)

	if err := server.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() err = %v", err)
	}

	wantPayloads := [][]byte{
		[]byte("first-packet"),
		[]byte("second-packet"),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- client.WriteBatchTo(wantPayloads, serverUDP.LocalAddr().(*net.UDPAddr).AddrPort())
	}()

	msgs := makeBatchMessages(2, 128)
	var gotPayloads [][]byte
	for reads := 0; reads < 4 && len(gotPayloads) < len(wantPayloads); reads++ {
		n, err := server.ReadBatch(msgs, 0)
		if err != nil {
			t.Fatalf("ReadBatch() err = %v", err)
		}
		for i := 0; i < n; i++ {
			if msgs[i].N == 0 {
				continue
			}
			got := append([]byte(nil), msgs[i].Buffers[0][:msgs[i].N]...)
			gotPayloads = append(gotPayloads, got)
			addr, ok := msgs[i].Addr.(*net.UDPAddr)
			if !ok {
				t.Fatalf("msgs[%d].Addr type = %T, want *net.UDPAddr", i, msgs[i].Addr)
			}
			if gotAddr, wantAddr := addr.AddrPort(), clientUDP.LocalAddr().(*net.UDPAddr).AddrPort(); gotAddr != wantAddr {
				t.Fatalf("msgs[%d].Addr = %v, want %v", i, gotAddr, wantAddr)
			}
		}
	}

	if err := <-errCh; err != nil {
		t.Fatalf("WriteBatchTo() err = %v", err)
	}
	if len(gotPayloads) != len(wantPayloads) {
		t.Fatalf("received %d payloads, want %d", len(gotPayloads), len(wantPayloads))
	}
	for i := range wantPayloads {
		if got, want := string(gotPayloads[i]), string(wantPayloads[i]); got != want {
			t.Fatalf("payload[%d] = %q, want %q", i, got, want)
		}
	}
}

func TestBatchingConnSinglePacketReadsUnsupported(t *testing.T) {
	udpConn := listenUDP4(t)
	bc := TryUpgradeToBatchingConn(udpConn, "udp4", 1).(BatchingConn)

	if _, _, err := bc.ReadFromUDPAddrPort(make([]byte, 16)); !errors.Is(err, ErrSinglePacketReadUnsupported) {
		t.Fatalf("ReadFromUDPAddrPort() err = %v, want %v", err, ErrSinglePacketReadUnsupported)
	}
	if _, _, err := bc.ReadFromUDP(make([]byte, 16)); !errors.Is(err, ErrSinglePacketReadUnsupported) {
		t.Fatalf("ReadFromUDP() err = %v, want %v", err, ErrSinglePacketReadUnsupported)
	}
}

func listenUDP4(t *testing.T) *net.UDPConn {
	t.Helper()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("ListenUDP() err = %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})
	return conn
}

func makeBatchMessages(n, mtu int) []ipv6.Message {
	msgs := make([]ipv6.Message, n)
	for i := range msgs {
		msgs[i].Buffers = net.Buffers{make([]byte, mtu)}
		msgs[i].OOB = make([]byte, 0, MinControlMessageSize())
	}
	return msgs
}
