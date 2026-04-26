package conn

import (
	"io"
	"sync/atomic"
)

type closerTracker interface {
	Register(id uint64, c io.Closer) error
	Unregister(id uint64)
}

var nextCloserID atomic.Uint64
