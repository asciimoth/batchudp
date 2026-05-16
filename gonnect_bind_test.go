package conn

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/asciimoth/gonnect"
)

type listenCall struct {
	network string
	laddr   string
}

type recordingNetwork struct {
	*gonnect.NativeNetwork

	mu                   sync.Mutex
	listenUDPCalls       []listenCall
	listenUDPConfigCalls []listenCall
}

func newRecordingNetwork() *recordingNetwork {
	return &recordingNetwork{
		NativeNetwork: (&gonnect.NativeConfig{}).Build(),
	}
}

func (n *recordingNetwork) ListenUDP(
	ctx context.Context,
	network, laddr string,
) (gonnect.UDPConn, error) {
	n.mu.Lock()
	n.listenUDPCalls = append(n.listenUDPCalls, listenCall{
		network: network,
		laddr:   laddr,
	})
	n.mu.Unlock()
	return n.NativeNetwork.ListenUDP(ctx, network, laddr)
}

func (n *recordingNetwork) ListenUDPConfig(
	ctx context.Context,
	lc *gonnect.ListenConfig,
	network, laddr string,
) (gonnect.UDPConn, error) {
	n.mu.Lock()
	n.listenUDPConfigCalls = append(n.listenUDPConfigCalls, listenCall{
		network: network,
		laddr:   laddr,
	})
	n.mu.Unlock()
	return n.NativeNetwork.ListenUDPConfig(ctx, lc, network, laddr)
}

func TestNewDefaultBindUsesProvidedNetwork(t *testing.T) {
	network := newRecordingNetwork()

	bind := NewDefaultBind(network)

	std, ok := bind.(*StdNetBind)
	if !ok {
		t.Fatalf("NewDefaultBind() type = %T, want *StdNetBind", bind)
	}
	if std.network != network {
		t.Fatal("StdNetBind did not retain provided network")
	}
}

func TestStdNetBindOpenUsesListenUDPConfigWhenAvailable(t *testing.T) {
	network := newRecordingNetwork()
	bind := NewStdNetBind(network).(*StdNetBind)

	fns, _, err := bind.Open(0)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer bind.Close()

	if len(fns) == 0 {
		t.Fatal("Open() returned no receive funcs")
	}

	network.mu.Lock()
	defer network.mu.Unlock()

	if len(network.listenUDPCalls) != 0 {
		t.Fatalf("ListenUDP() calls = %d, want 0", len(network.listenUDPCalls))
	}
	if len(network.listenUDPConfigCalls) != 2 {
		t.Fatalf("ListenUDPConfig() calls = %d, want 2", len(network.listenUDPConfigCalls))
	}
	if got := network.listenUDPConfigCalls[0].network; got != "udp4" {
		t.Fatalf("first ListenUDPConfig() network = %q, want udp4", got)
	}
	if got := network.listenUDPConfigCalls[1].network; got != "udp6" {
		t.Fatalf("second ListenUDPConfig() network = %q, want udp6", got)
	}
}

func TestListenConfigControlIgnoresNilRawConn(t *testing.T) {
	if err := listenConfig().Control("udp6", "[::]:0", nil); err != nil {
		t.Fatalf("Control() error = %v, want nil", err)
	}
}

func TestWinRingCloserSubscriberRequiresNativeSubscribeCloser(t *testing.T) {
	nativeNetwork := (&gonnect.NativeConfig{}).Build()
	if _, ok := winRingCloserSubscriber(nativeNetwork); ok {
		t.Fatal("native network without SubscribeCloser was accepted")
	}

	detachedNative := gonnect.DetachNetwork(nativeNetwork)
	if _, ok := winRingCloserSubscriber(detachedNative); !ok {
		t.Fatal("detached native network was rejected")
	}

	detachedLoopback := gonnect.DetachNetwork(gonnect.NewLoopbackNetwok())
	if _, ok := winRingCloserSubscriber(detachedLoopback); ok {
		t.Fatal("detached non-native network was accepted")
	}
}

func TestNativeNetworkDownClosesStdNetBind(t *testing.T) {
	network := gonnect.DetachNetwork((&gonnect.NativeConfig{}).Build())
	bind := NewStdNetBind(network).(*StdNetBind)

	fns, _, err := bind.Open(0)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if err := network.Down(); err != nil {
		t.Fatalf("Down() error = %v", err)
	}

	bufs := [][]byte{make([]byte, 1)}
	sizes := make([]int, 1)
	eps := make([]Endpoint, 1)
	for i, fn := range fns {
		_, err := fn(bufs, sizes, eps)
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("ReceiveFunc[%d] err = %v, want net.ErrClosed", i, err)
		}
	}
}
