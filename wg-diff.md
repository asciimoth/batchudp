# Differences from wireguard-go
Differences from the [wireguard-go/conn](https://github.com/WireGuard/wireguard-go/tree/master/conn):

- `NewStdNetBind` and `NewDefaultBind` take an explicit `gonnect.Network`.
- `StdNetBind` opens sockets through the supplied `gonnect.Network` instead of
  calling the standard library listen APIs directly.
- Linux batch I/O, sticky ancillary data, socket marks, and similar raw-socket
  features are now capability-gated so wrapped or virtual gonnect UDP
  connections can fall back to ordinary `ReadMsgUDP` / `WriteMsgUDP`.
- On Windows, `WinRingBind` is only selected when the supplied network unwraps
  to `gonnect/native.Network`; otherwise `NewDefaultBind` falls back to
  `StdNetBind`.
- When `WinRingBind` is selected with `gonnect/native.Network`, it registers
  itself for external closer tracking so `Network.Down()` closes the bind.
- Receive functions now return `ErrReadBufferTooShort` when the caller supplies
  fewer than `BatchSize()` packet, size, or endpoint slots.
