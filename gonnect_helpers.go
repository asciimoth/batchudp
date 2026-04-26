package conn

import (
	"net"

	"github.com/asciimoth/gonnect"
	gnative "github.com/asciimoth/gonnect/native"
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

func unwrapNativeNetwork(network gonnect.Network) *gnative.Network {
	if network == nil {
		return nil
	}
	native, _ := unwrapValue(network).(*gnative.Network)
	return native
}
