# BatchUDP

`batchudp` is a small UDP transport package extracted from `wireguard-go/conn`.
It is for applications that want the WireGuard-style `Bind` API without pulling
in the rest of `wireguard-go`.

It opens IPv4 and IPv6 UDP sockets on the same port, exposes per-family receive
functions, and hides platform-specific details such as batch I/O, sticky source
address handling, and Linux UDP GSO/GRO support.

> [!IMPORTANT]
> This project contains code extracted from the original
> [wireguard-go](https://git.zx2c4.com/wireguard-go) project
> with some modifications.
> All credit goes to the original wireguard-go authors.

## Usage

```go
package main

import (
	"log"

	conn "github.com/asciimoth/batchudp"
	"github.com/asciimoth/gonnect/native"
)

func main() {
	network := (&native.Config{}).Build()
	defer network.Down()

	bind := conn.NewDefaultBind(network)
	recvFns, port, err := bind.Open(0)
	if err != nil {
		log.Fatal(err)
	}
	defer bind.Close()

	peer, err := bind.ParseEndpoint("127.0.0.1:9000")
	if err != nil {
		log.Fatal(err)
	}

	if err := bind.Send([][]byte{[]byte("ping")}, peer); err != nil {
		log.Fatal(err)
	}

	packets := make([][]byte, bind.BatchSize())
	sizes := make([]int, bind.BatchSize())
	eps := make([]conn.Endpoint, bind.BatchSize())
	for i := range packets {
		packets[i] = make([]byte, 2048)
	}

	n, err := recvFns[0](packets, sizes, eps)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("listening on %d, received %d packet(s)", port, n)
}
```

For a larger end-to-end example, see [`examples/udp_pingpong`](examples/udp_pingpong).

## Migration From `wireguard-go/conn`

Most of the public `Bind`, `Endpoint`, and `ReceiveFunc` shape is intentionally
similar, so send/receive code usually moves over with little change.

The main differences are:

- `NewDefaultBind` and `NewStdNetBind` require a `gonnect.Network`.
- `NewStdNetBindWithOptions` and `NewDefaultBindWithOptions` let callers opt
  into IPv6-first opens or single-family fallback for networks that cannot
  expose both UDP families.
- Network lifecycle is explicit. When using `gonnect/native`, call
  `network.Down()` when the network should be torn down.
- If you start from an existing `gonnect.PacketConn` on Linux, use
  `TryUpgradeToBatchingConn` to opt into batched reads and writes.

Typical constructor migration:

```go
// wireguard-go/conn
bind := conn.NewDefaultBind()
```

```go
// batchudp
network := (&native.Config{}).Build()
bind := conn.NewDefaultBind(network)
defer network.Down()
```

If your old code only depended on the `Bind` interface after construction, the
rest of the call sites should usually keep the same structure:
`Open`, `ParseEndpoint`, `Send`, `BatchSize`, and the returned `ReceiveFunc`
values all work the same way.
