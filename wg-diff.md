# Differences from wireguard-go
Differences from the [wireguard-go/conn](https://github.com/WireGuard/wireguard-go/tree/master/conn):

- `NewStdNetBind` and `NewDefaultBind` take an explicit `gonnect.Network`.
- `StdNetBind` opens sockets through the supplied `gonnect.Network` instead of
  calling the standard library listen APIs directly.
- Linux batch I/O, sticky ancillary data, socket marks, and similar raw-socket
  features are now capability-gated so wrapped or virtual gonnect UDP
  connections can fall back to ordinary `ReadMsgUDP` /
  `WriteMsgUDPAddrPort`.
- Non-batch `StdNetBind` sends now route through
  `gonnect.UDPConn.WriteMsgUDPAddrPort`, which keeps virtual gonnect UDP
  networks such as loopback compatible with endpoint sends.
- `StdNetBind` normalizes received IPv4-mapped IPv6 peer addresses back to
  plain IPv4 before exposing them as endpoints.
- On Windows, `WinRingBind` is only selected when the supplied network unwraps
  to `gonnect/native.Network`; otherwise `NewDefaultBind` falls back to
  `StdNetBind`.
- When `WinRingBind` is selected with `gonnect/native.Network`, it registers
  itself for external closer tracking so `Network.Down()` closes the bind.
- Receive functions now return `ErrReadBufferTooShort` when the caller supplies
  fewer than `BatchSize()` packet, size, or endpoint slots.
