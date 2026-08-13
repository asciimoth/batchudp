package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	conn "github.com/asciimoth/batchudp"
	"github.com/asciimoth/batchudp/bindtest"
	"github.com/asciimoth/gonnect"
)

const maxUDPPayload = 65507

type config struct {
	mode        string
	sessions    int
	roundTrips  int
	payloadSize int
	timeout     time.Duration
}

type scenario struct {
	name    string
	factory func() (pairFactory, error)
}

type pairFactory interface {
	NewPair() (conn.Bind, conn.Bind, error)
	Close() error
}

type closeFunc func() error

func (fn closeFunc) Close() error { return fn() }

type taskGroup struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	wg     sync.WaitGroup

	errMu sync.Mutex
	err   error
}

func newTaskGroup(parent context.Context) *taskGroup {
	ctx, cancel := context.WithCancelCause(parent)
	return &taskGroup{
		ctx:    ctx,
		cancel: cancel,
	}
}

func (g *taskGroup) Go(fn func(context.Context) error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		if err := fn(g.ctx); err != nil && !errors.Is(err, context.Canceled) {
			g.fail(err)
		}
	}()
}

func (g *taskGroup) fail(err error) {
	g.errMu.Lock()
	defer g.errMu.Unlock()
	if g.err != nil {
		return
	}
	g.err = err
	g.cancel(err)
}

func (g *taskGroup) Wait() error {
	g.wg.Wait()
	if g.err != nil {
		return g.err
	}
	if err := context.Cause(g.ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

type sharedNetworkFactory struct {
	newBind func() conn.Bind
	close   closeFunc
}

func (f *sharedNetworkFactory) NewPair() (conn.Bind, conn.Bind, error) {
	return f.newBind(), f.newBind(), nil
}

func (f *sharedNetworkFactory) Close() error {
	if f.close == nil {
		return nil
	}
	return f.close()
}

type channelFactory struct{}

func (channelFactory) NewPair() (conn.Bind, conn.Bind, error) {
	pair := bindtest.NewChannelBinds()
	return pair[0], pair[1], nil
}

func (channelFactory) Close() error { return nil }

var scenarios = []scenario{
	{
		name: "channel",
		factory: func() (pairFactory, error) {
			return channelFactory{}, nil
		},
	},
	{
		name: "native",
		factory: func() (pairFactory, error) {
			network := (&gonnect.NativeConfig{}).Build()
			return &sharedNetworkFactory{
				newBind: func() conn.Bind { return conn.NewDefaultBind(network) },
			}, nil
		},
	},
	{
		name: "loopback",
		factory: func() (pairFactory, error) {
			network := gonnect.NewLoopbackNetwork()
			return &sharedNetworkFactory{
				newBind: func() conn.Bind { return conn.NewDefaultBind(network) },
				close:   network.Down,
			}, nil
		},
	},
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.mode, "mode", "all", "scenario to run: channel, native, loopback, all")
	flag.IntVar(&cfg.sessions, "sessions", 64, "parallel ping-pong sessions per scenario")
	flag.IntVar(&cfg.roundTrips, "round-trips", 128, "ping-pong exchanges per session")
	flag.IntVar(&cfg.payloadSize, "payload-size", 60*1024, "UDP payload size in bytes")
	flag.DurationVar(&cfg.timeout, "timeout", 45*time.Second, "scenario timeout")
	flag.Parse()

	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "udp ping-pong failed: %v\n", err)
		os.Exit(1)
	}
}

func run(cfg config) error {
	if cfg.sessions <= 0 {
		return fmt.Errorf("sessions must be positive")
	}
	if cfg.roundTrips <= 0 {
		return fmt.Errorf("round-trips must be positive")
	}
	if cfg.payloadSize < 16 {
		return fmt.Errorf("payload-size must be at least 16 bytes")
	}

	selected, err := selectScenarios(cfg.mode)
	if err != nil {
		return err
	}
	if cfg.payloadSize > maxUDPPayload {
		for _, sc := range selected {
			if sc.name != "channel" {
				return fmt.Errorf(
					"payload-size %d exceeds max UDP payload %d for %s mode",
					cfg.payloadSize,
					maxUDPPayload,
					sc.name,
				)
			}
		}
	}

	for _, sc := range selected {
		started := time.Now()
		if err := runScenario(cfg, sc); err != nil {
			return fmt.Errorf("%s: %w", sc.name, err)
		}
		fmt.Printf(
			"scenario=%s sessions=%d round_trips=%d payload=%dB duration=%s\n",
			sc.name,
			cfg.sessions,
			cfg.roundTrips,
			cfg.payloadSize,
			time.Since(started).Round(time.Millisecond),
		)
	}
	return nil
}

func selectScenarios(mode string) ([]scenario, error) {
	if mode == "all" {
		return scenarios, nil
	}
	for _, sc := range scenarios {
		if sc.name == mode {
			return []scenario{sc}, nil
		}
	}
	return nil, fmt.Errorf("unknown mode %q", mode)
}

func runScenario(cfg config, sc scenario) error {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	factory, err := sc.factory()
	if err != nil {
		return err
	}
	defer func() { _ = factory.Close() }()

	group := newTaskGroup(ctx)
	for sessionID := 0; sessionID < cfg.sessions; sessionID++ {
		sessionID := sessionID
		group.Go(func(ctx context.Context) error {
			return runSession(ctx, factory, sc.name, cfg.payloadSize, cfg.roundTrips, sessionID)
		})
	}

	return group.Wait()
}

func runSession(
	ctx context.Context,
	factory pairFactory,
	mode string,
	payloadSize int,
	roundTrips int,
	sessionID int,
) error {
	clientBind, serverBind, err := factory.NewPair()
	if err != nil {
		return err
	}
	defer func() { _ = clientBind.Close() }()
	defer func() { _ = serverBind.Close() }()

	serverFuncs, serverPort, err := serverBind.Open(0)
	if err != nil {
		return fmt.Errorf("open server bind: %w", err)
	}
	clientFuncs, _, err := clientBind.Open(0)
	if err != nil {
		return fmt.Errorf("open client bind: %w", err)
	}

	serverGroup := newTaskGroup(ctx)
	for _, fn := range serverFuncs {
		fn := fn
		serverGroup.Go(func(ctx context.Context) error {
			return receiveLoop(ctx, serverBind, fn, func(packet []byte, ep conn.Endpoint) error {
				reply := append([]byte(nil), packet...)
				return serverBind.Send([][]byte{reply}, ep)
			})
		})
	}

	clientRecv := make(chan []byte, 1)
	clientGroup := newTaskGroup(ctx)
	for _, fn := range clientFuncs {
		fn := fn
		clientGroup.Go(func(ctx context.Context) error {
			return receiveLoop(ctx, clientBind, fn, func(packet []byte, _ conn.Endpoint) error {
				reply := append([]byte(nil), packet...)
				select {
				case clientRecv <- reply:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		})
	}

	serverEndpoint, err := clientBind.ParseEndpoint(net.JoinHostPort("127.0.0.1", fmt.Sprint(serverPort)))
	if err != nil {
		return fmt.Errorf("parse server endpoint: %w", err)
	}

	for roundTrip := 0; roundTrip < roundTrips; roundTrip++ {
		payload := makePayload(payloadSize, sessionID, roundTrip)
		if err := clientBind.Send([][]byte{payload}, serverEndpoint); err != nil {
			return fmt.Errorf("send ping: %w", err)
		}

		select {
		case reply := <-clientRecv:
			if !bytes.Equal(reply, payload) {
				return fmt.Errorf(
					"payload mismatch in mode=%s session=%d round_trip=%d",
					mode,
					sessionID,
					roundTrip,
				)
			}
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}

	if err := clientBind.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	if err := serverBind.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	if err := clientGroup.Wait(); err != nil {
		return err
	}
	if err := serverGroup.Wait(); err != nil {
		return err
	}
	return nil
}

func receiveLoop(
	ctx context.Context,
	bind conn.Bind,
	fn conn.ReceiveFunc,
	handle func(packet []byte, ep conn.Endpoint) error,
) error {
	batchSize := bind.BatchSize()
	packets := make([][]byte, batchSize)
	sizes := make([]int, batchSize)
	eps := make([]conn.Endpoint, batchSize)
	for i := range packets {
		packets[i] = make([]byte, maxPacketBuffer(bind.BatchSize()))
	}

	for {
		clear(sizes)
		clear(eps)
		n, err := fn(packets, sizes, eps)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
				return nil
			}
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for i := 0; i < n; i++ {
			if sizes[i] == 0 || eps[i] == nil {
				continue
			}
			if err := handle(packets[i][:sizes[i]], eps[i]); err != nil {
				if errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			}
		}
	}
}

func maxPacketBuffer(batchSize int) int {
	if batchSize > 1 {
		return maxUDPPayload
	}
	return maxUDPPayload
}

func makePayload(payloadSize int, sessionID int, roundTrip int) []byte {
	payload := make([]byte, payloadSize)
	binary.LittleEndian.PutUint64(payload[0:8], uint64(sessionID))
	binary.LittleEndian.PutUint64(payload[8:16], uint64(roundTrip))
	for i := 16; i < len(payload); i++ {
		payload[i] = byte((sessionID*31 + roundTrip*17 + i) & 0xff)
	}
	return payload
}
