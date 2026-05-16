package conn

import "io"

type closerSubscriber interface {
	SubscribeCloser(c io.Closer) (func(), error)
}

func winRingCloserSubscriber(network interface {
	IsNative() bool
}) (closerSubscriber, bool) {
	if network == nil || !network.IsNative() {
		return nil, false
	}
	subscriber, ok := network.(closerSubscriber)
	return subscriber, ok
}
