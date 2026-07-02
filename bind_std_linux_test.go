//go:build linux

package conn

import (
	"testing"

	"github.com/asciimoth/gonnect"
	"golang.org/x/net/ipv6"
)

type recordingBatchReader struct {
	readLens []int
}

func (r *recordingBatchReader) ReadBatch(msgs []ipv6.Message, _ int) (int, error) {
	r.readLens = append(r.readLens, len(msgs))
	return 0, nil
}

func TestStdNetBindReceiveUsesConfiguredReadBatchSize(t *testing.T) {
	const batchSize = 3

	bind := NewStdNetBindWithOptions((&gonnect.NativeConfig{}).Build(), StdNetBindOptions{
		BatchSize: batchSize,
	}).(*StdNetBind)
	br := &recordingBatchReader{}
	bufs := make([][]byte, batchSize+2)
	for i := range bufs {
		bufs[i] = make([]byte, 128)
	}
	sizes := make([]int, batchSize+2)
	eps := make([]Endpoint, batchSize+2)

	n, err := bind.receiveIP(br, nil, false, bufs, sizes, eps)
	if err != nil {
		t.Fatalf("receiveIP() err = %v", err)
	}
	if n != 0 {
		t.Fatalf("receiveIP() n = %d, want 0", n)
	}
	if got := br.readLens; len(got) != 1 || got[0] != batchSize {
		t.Fatalf("ReadBatch lens = %v, want [%d]", got, batchSize)
	}
}

func TestStdNetBindReceiveGROUsesConfiguredReadBatchWindow(t *testing.T) {
	const batchSize = 65

	bind := NewStdNetBindWithOptions((&gonnect.NativeConfig{}).Build(), StdNetBindOptions{
		BatchSize: batchSize,
	}).(*StdNetBind)
	br := &recordingBatchReader{}
	bufs := make([][]byte, batchSize)
	for i := range bufs {
		bufs[i] = make([]byte, 128)
	}
	sizes := make([]int, batchSize)
	eps := make([]Endpoint, batchSize)

	n, err := bind.receiveIP(br, nil, true, bufs, sizes, eps)
	if err != nil {
		t.Fatalf("receiveIP() err = %v", err)
	}
	if n != 0 {
		t.Fatalf("receiveIP() n = %d, want 0", n)
	}
	if got := br.readLens; len(got) != 1 || got[0] != 2 {
		t.Fatalf("ReadBatch lens = %v, want [2]", got)
	}
}

func TestGroReadBatchSize(t *testing.T) {
	cases := []struct {
		batchSize int
		want      int
	}{
		{batchSize: 1, want: 1},
		{batchSize: 2, want: 1},
		{batchSize: udpSegmentMaxDatagrams, want: 1},
		{batchSize: udpSegmentMaxDatagrams + 1, want: 2},
		{batchSize: IdealBatchSize, want: 2},
	}

	for _, tt := range cases {
		if got := groReadBatchSize(tt.batchSize); got != tt.want {
			t.Fatalf("groReadBatchSize(%d) = %d, want %d", tt.batchSize, got, tt.want)
		}
	}
}

var _ batchReader = (*recordingBatchReader)(nil)
