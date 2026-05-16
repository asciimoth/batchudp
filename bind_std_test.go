package conn

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/asciimoth/gonnect"
	"golang.org/x/net/ipv6"
)

func TestStdNetBindReceiveFuncAfterClose(t *testing.T) {
	bind := NewStdNetBind((&gonnect.NativeConfig{}).Build()).(*StdNetBind)
	fns, _, err := bind.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	bind.Close()
	bufs := make([][]byte, 1)
	bufs[0] = make([]byte, 1)
	sizes := make([]int, 1)
	eps := make([]Endpoint, 1)
	for _, fn := range fns {
		// The ReceiveFuncs must not access conn-related fields on StdNetBind
		// unguarded. Close() nils the conn-related fields resulting in a panic
		// if they violate the mutex.
		fn(bufs, sizes, eps)
	}
}

func TestStdNetBindReceiveFuncShortBufferList(t *testing.T) {
	bind := NewStdNetBind((&gonnect.NativeConfig{}).Build()).(*StdNetBind)
	fns, _, err := bind.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	defer bind.Close()

	shortLen := bind.BatchSize() - 1
	bufs := make([][]byte, shortLen)
	sizes := make([]int, shortLen)
	eps := make([]Endpoint, shortLen)
	for _, fn := range fns {
		n, err := fn(bufs, sizes, eps)
		if !errors.Is(err, ErrReadBufferTooShort) {
			t.Fatalf("ReceiveFunc err = %v, want %v", err, ErrReadBufferTooShort)
		}
		if n != 0 {
			t.Fatalf("ReceiveFunc n = %d, want 0", n)
		}
	}
}

func TestStdNetEndpointClearSrcRetainsBackingStorage(t *testing.T) {
	ep := &StdNetEndpoint{
		AddrPort: netip.MustParseAddrPort("127.0.0.1:1"),
		src:      make([]byte, 4, 32),
	}
	copy(ep.src, []byte{1, 2, 3, 4})

	ep.ClearSrc()

	if len(ep.src) != 0 {
		t.Fatalf("len(src) = %d, want 0", len(ep.src))
	}
	if cap(ep.src) != 32 {
		t.Fatalf("cap(src) = %d, want 32", cap(ep.src))
	}
}

func TestStdNetBindMessagePoolReset(t *testing.T) {
	bind := NewStdNetBind((&gonnect.NativeConfig{}).Build()).(*StdNetBind)
	msgs := bind.getMessages()

	(*msgs)[0].N = 123
	(*msgs)[0].NN = 9
	(*msgs)[0].Addr = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9000}
	(*msgs)[0].Buffers[0] = []byte{1, 2, 3}
	(*msgs)[0].OOB = append((*msgs)[0].OOB, 1, 2, 3)

	bind.putMessages(msgs)

	msgs = bind.getMessages()
	defer bind.putMessages(msgs)

	for i, msg := range *msgs {
		if msg.N != 0 {
			t.Fatalf("msgs[%d].N = %d, want 0", i, msg.N)
		}
		if msg.NN != 0 {
			t.Fatalf("msgs[%d].NN = %d, want 0", i, msg.NN)
		}
		if msg.Addr != nil {
			t.Fatalf("msgs[%d].Addr = %#v, want nil", i, msg.Addr)
		}
		if len(msg.Buffers) != 1 {
			t.Fatalf("msgs[%d].Buffers len = %d, want 1", i, len(msg.Buffers))
		}
		if len(msg.OOB) != 0 {
			t.Fatalf("msgs[%d].OOB len = %d, want 0", i, len(msg.OOB))
		}
	}
}

func mockSetGSOSize(control *[]byte, gsoSize uint16) {
	*control = (*control)[:cap(*control)]
	binary.LittleEndian.PutUint16(*control, gsoSize)
}

func Test_coalesceMessages(t *testing.T) {
	cases := []struct {
		name     string
		buffs    [][]byte
		wantLens []int
		wantGSO  []int
	}{
		{
			name: "one message no coalesce",
			buffs: [][]byte{
				make([]byte, 1, 1),
			},
			wantLens: []int{1},
			wantGSO:  []int{0},
		},
		{
			name: "two messages equal len coalesce",
			buffs: [][]byte{
				make([]byte, 1, 2),
				make([]byte, 1, 1),
			},
			wantLens: []int{2},
			wantGSO:  []int{1},
		},
		{
			name: "two messages unequal len coalesce",
			buffs: [][]byte{
				make([]byte, 2, 3),
				make([]byte, 1, 1),
			},
			wantLens: []int{3},
			wantGSO:  []int{2},
		},
		{
			name: "three messages second unequal len coalesce",
			buffs: [][]byte{
				make([]byte, 2, 3),
				make([]byte, 1, 1),
				make([]byte, 2, 2),
			},
			wantLens: []int{3, 2},
			wantGSO:  []int{2, 0},
		},
		{
			name: "three messages limited cap coalesce",
			buffs: [][]byte{
				make([]byte, 2, 4),
				make([]byte, 2, 2),
				make([]byte, 2, 2),
			},
			wantLens: []int{4, 2},
			wantGSO:  []int{2, 0},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			addr := &net.UDPAddr{
				IP:   net.ParseIP("127.0.0.1").To4(),
				Port: 1,
			}
			msgs := make([]ipv6.Message, len(tt.buffs))
			for i := range msgs {
				msgs[i].Buffers = make([][]byte, 1)
				msgs[i].OOB = make([]byte, 0, 2)
			}
			got := coalesceMessages(addr, &StdNetEndpoint{AddrPort: addr.AddrPort()}, tt.buffs, msgs, mockSetGSOSize)
			if got != len(tt.wantLens) {
				t.Fatalf("got len %d want: %d", got, len(tt.wantLens))
			}
			for i := 0; i < got; i++ {
				if msgs[i].Addr != addr {
					t.Errorf("msgs[%d].Addr != passed addr", i)
				}
				gotLen := len(msgs[i].Buffers[0])
				if gotLen != tt.wantLens[i] {
					t.Errorf("len(msgs[%d].Buffers[0]) %d != %d", i, gotLen, tt.wantLens[i])
				}
				gotGSO, err := mockGetGSOSize(msgs[i].OOB)
				if err != nil {
					t.Fatalf("msgs[%d] getGSOSize err: %v", i, err)
				}
				if gotGSO != tt.wantGSO[i] {
					t.Errorf("msgs[%d] gsoSize %d != %d", i, gotGSO, tt.wantGSO[i])
				}
			}
		})
	}
}

func mockGetGSOSize(control []byte) (int, error) {
	if len(control) < 2 {
		return 0, nil
	}
	return int(binary.LittleEndian.Uint16(control)), nil
}

func Test_splitCoalescedMessages(t *testing.T) {
	newMsg := func(n, gso int) ipv6.Message {
		msg := ipv6.Message{
			Buffers: [][]byte{make([]byte, 1<<16-1)},
			N:       n,
			OOB:     make([]byte, 2),
		}
		binary.LittleEndian.PutUint16(msg.OOB, uint16(gso))
		if gso > 0 {
			msg.NN = 2
		}
		return msg
	}

	cases := []struct {
		name        string
		msgs        []ipv6.Message
		firstMsgAt  int
		wantNumEval int
		wantMsgLens []int
		wantErr     bool
	}{
		{
			name: "second last split last empty",
			msgs: []ipv6.Message{
				newMsg(0, 0),
				newMsg(0, 0),
				newMsg(3, 1),
				newMsg(0, 0),
			},
			firstMsgAt:  2,
			wantNumEval: 3,
			wantMsgLens: []int{1, 1, 1, 0},
			wantErr:     false,
		},
		{
			name: "second last no split last empty",
			msgs: []ipv6.Message{
				newMsg(0, 0),
				newMsg(0, 0),
				newMsg(1, 0),
				newMsg(0, 0),
			},
			firstMsgAt:  2,
			wantNumEval: 1,
			wantMsgLens: []int{1, 0, 0, 0},
			wantErr:     false,
		},
		{
			name: "second last no split last no split",
			msgs: []ipv6.Message{
				newMsg(0, 0),
				newMsg(0, 0),
				newMsg(1, 0),
				newMsg(1, 0),
			},
			firstMsgAt:  2,
			wantNumEval: 2,
			wantMsgLens: []int{1, 1, 0, 0},
			wantErr:     false,
		},
		{
			name: "second last no split last split",
			msgs: []ipv6.Message{
				newMsg(0, 0),
				newMsg(0, 0),
				newMsg(1, 0),
				newMsg(3, 1),
			},
			firstMsgAt:  2,
			wantNumEval: 4,
			wantMsgLens: []int{1, 1, 1, 1},
			wantErr:     false,
		},
		{
			name: "second last split last split",
			msgs: []ipv6.Message{
				newMsg(0, 0),
				newMsg(0, 0),
				newMsg(2, 1),
				newMsg(2, 1),
			},
			firstMsgAt:  2,
			wantNumEval: 4,
			wantMsgLens: []int{1, 1, 1, 1},
			wantErr:     false,
		},
		{
			name: "second last no split last split overflow",
			msgs: []ipv6.Message{
				newMsg(0, 0),
				newMsg(0, 0),
				newMsg(1, 0),
				newMsg(4, 1),
			},
			firstMsgAt:  2,
			wantNumEval: 4,
			wantMsgLens: []int{1, 1, 1, 1},
			wantErr:     true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitCoalescedMessages(tt.msgs, 2, mockGetGSOSize)
			if err != nil && !tt.wantErr {
				t.Fatalf("err: %v", err)
			}
			if got != tt.wantNumEval {
				t.Fatalf("got to eval: %d want: %d", got, tt.wantNumEval)
			}
			for i, msg := range tt.msgs {
				if msg.N != tt.wantMsgLens[i] {
					t.Fatalf("msg[%d].N: %d want: %d", i, msg.N, tt.wantMsgLens[i])
				}
			}
		})
	}
}

func Test_splitCoalescedMessagesPreservesPayloadAndAddr(t *testing.T) {
	msgs := make([]ipv6.Message, 4)
	for i := range msgs {
		msgs[i].Buffers = [][]byte{make([]byte, 8)}
	}

	src := []byte{1, 2, 3, 4, 5, 6}
	copy(msgs[3].Buffers[0], src)
	msgs[3].N = len(src)
	msgs[3].NN = 2
	msgs[3].OOB = make([]byte, 2)
	binary.LittleEndian.PutUint16(msgs[3].OOB, 2)
	addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}
	msgs[3].Addr = addr

	n, err := splitCoalescedMessages(msgs, 3, mockGetGSOSize)
	if err != nil {
		t.Fatalf("splitCoalescedMessages err: %v", err)
	}
	if n != 3 {
		t.Fatalf("splitCoalescedMessages n = %d, want 3", n)
	}

	wantPayloads := [][]byte{
		{1, 2},
		{3, 4},
		{5, 6},
	}
	for i, want := range wantPayloads {
		got := msgs[i].Buffers[0][:msgs[i].N]
		if msgs[i].Addr != addr {
			t.Fatalf("msgs[%d].Addr = %#v, want %#v", i, msgs[i].Addr, addr)
		}
		if string(got) != string(want) {
			t.Fatalf("msgs[%d] payload = %v, want %v", i, got, want)
		}
	}
}

func Test_splitCoalescedMessagesPropagatesGSOParseError(t *testing.T) {
	msgs := make([]ipv6.Message, 1)
	msgs[0].Buffers = [][]byte{make([]byte, 4)}
	msgs[0].N = 4
	msgs[0].NN = 1
	msgs[0].OOB = []byte{1}

	wantErr := errors.New("boom")
	_, err := splitCoalescedMessages(msgs, 0, func(control []byte) (int, error) {
		return 0, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
