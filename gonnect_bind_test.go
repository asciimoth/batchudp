package conn

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strconv"
	"sync"
	"syscall"
	"testing"

	"github.com/asciimoth/gonnect"
)

func closeBind(t *testing.T, bind Bind) {
	t.Helper()
	if err := bind.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

type listenCall struct {
	network string
	laddr   string
}

type recordingNetwork struct {
	*gonnect.NativeNetwork

	mu                   sync.Mutex
	listenUDPCalls       []listenCall
	listenUDPConfigCalls []listenCall
	listenUDPConfigErrs  map[string]error
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
	if err := n.listenUDPConfigErrs[network]; err != nil {
		return nil, err
	}
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
	defer closeBind(t, bind)

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

func TestStdNetBindOpenStrictSiblingFailure(t *testing.T) {
	wantErr := errors.New("udp6 unavailable")
	network := newRecordingNetwork()
	network.listenUDPConfigErrs = map[string]error{"udp6": wantErr}
	bind := NewStdNetBind(network).(*StdNetBind)

	fns, _, err := bind.Open(0)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Open() error = %v, want %v", err, wantErr)
	}
	if fns != nil {
		t.Fatalf("Open() fns = %d, want nil", len(fns))
	}
	if bind.ipv4 != nil || bind.ipv6 != nil {
		t.Fatal("Open() left sockets installed after strict sibling failure")
	}
}

func TestStdNetBindOpenAllowSingleFamilySiblingFailure(t *testing.T) {
	wantErr := errors.New("udp6 unavailable")
	network := newRecordingNetwork()
	network.listenUDPConfigErrs = map[string]error{"udp6": wantErr}

	var callbackNetwork string
	var callbackErr error
	bind := NewStdNetBindWithOptions(network, StdNetBindOptions{
		AllowSingleFamily: true,
		OnFamilyOpenError: func(network string, err error) {
			callbackNetwork = network
			callbackErr = err
		},
	}).(*StdNetBind)

	fns, port, err := bind.Open(0)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer closeBind(t, bind)

	if len(fns) != 1 {
		t.Fatalf("Open() fns = %d, want 1", len(fns))
	}
	if port == 0 {
		t.Fatal("Open() returned port 0 after preferred family opened")
	}
	if bind.ipv4 == nil {
		t.Fatal("Open() did not keep udp4 open")
	}
	if bind.ipv6 != nil {
		t.Fatal("Open() unexpectedly installed udp6")
	}
	if callbackNetwork != "udp6" {
		t.Fatalf("OnFamilyOpenError network = %q, want udp6", callbackNetwork)
	}
	if !errors.Is(callbackErr, wantErr) {
		t.Fatalf("OnFamilyOpenError err = %v, want %v", callbackErr, wantErr)
	}
}

func TestStdNetBindOpenFamilyOrder(t *testing.T) {
	tests := []struct {
		name string
		opts StdNetBindOptions
		want []string
	}{
		{
			name: "ipv4 first",
			opts: StdNetBindOptions{FamilyOrder: FamilyOrderIPv4First},
			want: []string{"udp4", "udp6"},
		},
		{
			name: "ipv6 first",
			opts: StdNetBindOptions{FamilyOrder: FamilyOrderIPv6First},
			want: []string{"udp6", "udp4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network := newRecordingNetwork()
			bind := NewStdNetBindWithOptions(network, tt.opts).(*StdNetBind)

			_, _, err := bind.Open(0)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			defer closeBind(t, bind)

			network.mu.Lock()
			var got []string
			for _, call := range network.listenUDPConfigCalls {
				got = append(got, call.network)
			}
			network.mu.Unlock()

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ListenUDPConfig networks = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStdNetBindOpenUsesFirstFamilyActualPortForSibling(t *testing.T) {
	network := newRecordingNetwork()
	bind := NewStdNetBindWithOptions(network, StdNetBindOptions{
		FamilyOrder: FamilyOrderIPv6First,
	}).(*StdNetBind)

	_, actualPort, err := bind.Open(0)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer closeBind(t, bind)

	network.mu.Lock()
	calls := append([]listenCall(nil), network.listenUDPConfigCalls...)
	network.mu.Unlock()

	if len(calls) != 2 {
		t.Fatalf("ListenUDPConfig() calls = %d, want 2", len(calls))
	}
	if calls[0].network != "udp6" || calls[1].network != "udp4" {
		t.Fatalf("ListenUDPConfig() networks = %q, %q; want udp6, udp4", calls[0].network, calls[1].network)
	}
	_, firstPort, err := net.SplitHostPort(calls[0].laddr)
	if err != nil {
		t.Fatalf("SplitHostPort(first laddr) error = %v", err)
	}
	_, secondPort, err := net.SplitHostPort(calls[1].laddr)
	if err != nil {
		t.Fatalf("SplitHostPort(second laddr) error = %v", err)
	}
	if firstPort != "0" {
		t.Fatalf("first laddr port = %q, want 0", firstPort)
	}
	if secondPort == "0" {
		t.Fatal("second laddr port = 0, want first family's actual port")
	}
	if secondPort != strconv.Itoa(int(actualPort)) {
		t.Fatalf("second laddr port = %q, want actual port %d", secondPort, actualPort)
	}
}

func TestStdNetBindSendToUnopenedFamily(t *testing.T) {
	network := newRecordingNetwork()
	network.listenUDPConfigErrs = map[string]error{"udp6": errors.New("udp6 unavailable")}
	bind := NewStdNetBindWithOptions(network, StdNetBindOptions{
		AllowSingleFamily: true,
	}).(*StdNetBind)

	_, _, err := bind.Open(0)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer closeBind(t, bind)

	ep, err := bind.ParseEndpoint("[::1]:1")
	if err != nil {
		t.Fatalf("ParseEndpoint() error = %v", err)
	}
	err = bind.Send([][]byte{{1}}, ep)
	if !errors.Is(err, syscall.EAFNOSUPPORT) {
		t.Fatalf("Send() error = %v, want %v", err, syscall.EAFNOSUPPORT)
	}
}

func TestStdNetBindOpenBothFamiliesFail(t *testing.T) {
	wantErr := errors.New("udp6 unavailable")
	network := newRecordingNetwork()
	network.listenUDPConfigErrs = map[string]error{
		"udp4": syscall.EAFNOSUPPORT,
		"udp6": wantErr,
	}
	bind := NewStdNetBindWithOptions(network, StdNetBindOptions{
		AllowSingleFamily: true,
	}).(*StdNetBind)

	_, _, err := bind.Open(0)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Open() error = %v, want %v", err, wantErr)
	}

	network.mu.Lock()
	gotCalls := len(network.listenUDPConfigCalls)
	network.mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("ListenUDPConfig() calls = %d, want 2", gotCalls)
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

	detachedNative := gonnect.DetachNetwork(nativeNetwork, nil, nil)
	if _, ok := winRingCloserSubscriber(detachedNative); !ok {
		t.Fatal("detached native network was rejected")
	}

	detachedLoopback := gonnect.DetachNetwork(gonnect.NewLoopbackNetwork(), nil, nil)
	if _, ok := winRingCloserSubscriber(detachedLoopback); ok {
		t.Fatal("detached non-native network was accepted")
	}
}

func TestNativeNetworkDownClosesStdNetBind(t *testing.T) {
	network := gonnect.DetachNetwork((&gonnect.NativeConfig{}).Build(), nil, nil)
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
