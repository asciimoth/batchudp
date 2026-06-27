package conn

import (
	"net"

	"github.com/asciimoth/gonnect"
)

func unwrapValue(v any) any {
	for v != nil {
		next := gonnect.GetWrapped(v)
		if next == nil {
			return v
		}
		v = next
	}
	return nil
}

func unwrapUDPConn(conn gonnect.UDPConn) *net.UDPConn {
	if conn == nil {
		return nil
	}
	udp, _ := unwrapValue(conn).(*net.UDPConn)
	return udp
}
