# Differences from wireguard-go
Differences from the [wireguard-go/conn](https://github.com/WireGuard/wireguard-go/tree/master/conn):

- `NewStdNetBind` and `NewDefaultBind` take an explicit `gonnect.Network`.
- `StdNetBind` opens sockets through the supplied `gonnect.Network` instead of
  calling the standard library listen APIs directly.
- Added `StdNetBindOptions`, `NewStdNetBindWithOptions`, and
  `NewDefaultBindWithOptions` so callers can prefer IPv6 first or opt into
  single-family fallback when a sibling UDP family cannot be opened.
- Added `StdNetBindOptions.BatchSize` so callers can override `StdNetBind`'s
  effective batch size. Positive values drive `BatchSize()`, receive
  validation, native batch read/write slice lengths, and pooled message
  allocation; non-positive values preserve existing defaults.
- Linux batch I/O, sticky ancillary data, socket marks, and similar raw-socket
  features are now capability-gated so wrapped or virtual gonnect UDP
  connections can fall back to ordinary `ReadMsgUDP` /
  `WriteMsgUDPAddrPort`.
- Non-batch `StdNetBind` sends now route through
  `gonnect.UDPConn.WriteMsgUDPAddrPort`, which keeps virtual gonnect UDP
  networks such as loopback compatible with endpoint sends.
- `StdNetBind` normalizes received IPv4-mapped IPv6 peer addresses back to
  plain IPv4 before exposing them as endpoints.
- On Windows, `WinRingBind` is only selected when the supplied network is native
  and exposes `SubscribeCloser(io.Closer)`; otherwise `NewDefaultBind` falls
  back to `StdNetBind`.
- On Windows, `WinRingBind` sizes each RIO packet slot for full UDP payloads
  and uses a smaller ring depth than upstream to keep the total allocation
  bounded while supporting larger datagrams.
- When `WinRingBind` is selected, it subscribes itself for external closer
  tracking so `Network.Down()` closes the bind.
- gonnect listen control hooks may be ignored or called without raw-socket
  access; bind-time controls now tolerate a nil `syscall.RawConn`.
- Receive functions now return `ErrReadBufferTooShort` when the caller supplies
  fewer than `BatchSize()` packet, size, or endpoint slots.
- Added `TryUpgradeToBatchingConn`, a Linux-only upgrade path from a native
  backed `gonnect.PacketConn`/`gonnect.UDPConn` to a standalone batched UDP
  socket API with `ReadBatch` and `WriteBatchTo`.
