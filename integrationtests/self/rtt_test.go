package self_test

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	quicproxy "github.com/quic-go/quic-go/integrationtests/tools/proxy"
	"github.com/quic-go/quic-go/internal/synctest"
	"github.com/quic-go/quic-go/testutils/simnet"

	"github.com/stretchr/testify/require"
)

func runServerForRTTTest(t *testing.T) (net.Addr, <-chan error) {
	ln, err := quic.Listen(
		newUDPConnLocalhost(t),
		getTLSConfig(),
		getQuicConfig(nil),
	)
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	errChan := make(chan error, 1)
	go func() {
		defer close(errChan)
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil {
				errChan <- fmt.Errorf("accept error: %w", err)
				return
			}
			str, err := conn.OpenStream()
			if err != nil {
				errChan <- fmt.Errorf("open stream error: %w", err)
				return
			}
			_, err = str.Write(PRData)
			if err != nil {
				errChan <- fmt.Errorf("write error: %w", err)
				return
			}
			str.Close()
		}
	}()

	return ln.Addr(), errChan
}

func runServerForRTTTestWithConn(t *testing.T, conn net.PacketConn) (<-chan *quic.Conn, <-chan error) {
	ln, err := quic.Listen(
		conn,
		getTLSConfig(),
		getQuicConfig(nil),
	)
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	errChan := make(chan error, 1)
	connChan := make(chan *quic.Conn, 1)
	go func() {
		defer close(errChan)
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil {
				errChan <- fmt.Errorf("accept error: %w", err)
				return
			}
			connChan <- conn
			str, err := conn.OpenStream()
			if err != nil {
				errChan <- fmt.Errorf("open stream error: %w", err)
				return
			}
			if _, err := str.Write(PRData); err != nil {
				errChan <- fmt.Errorf("write error: %w", err)
				return
			}
			str.Close()
		}
	}()

	return connChan, errChan
}

func TestDownloadWithFixedRTT(t *testing.T) {
	for _, rtt := range []time.Duration{
		10 * time.Millisecond,
		100 * time.Millisecond,
		1000 * time.Millisecond,
	} {
		t.Run(fmt.Sprintf("RTT %s", rtt), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n := &simnet.Simnet{Router: &simnet.PerfectRouter{}}
				addrA := &net.UDPAddr{IP: net.ParseIP("1.0.0.1"), Port: 9001}
				addrB := &net.UDPAddr{IP: net.ParseIP("1.0.0.2"), Port: 9002}

				settings := simnet.NodeBiDiLinkSettings{
					Downlink: simnet.LinkSettings{BitsPerSecond: math.MaxInt, Latency: rtt / 4},
					Uplink:   simnet.LinkSettings{BitsPerSecond: math.MaxInt, Latency: rtt / 4},
				}
				clientConn := n.NewEndpoint(addrA, settings)
				defer clientConn.Close()
				serverConn := n.NewEndpoint(addrB, settings)
				defer serverConn.Close()

				connChan, errChan := runServerForRTTTestWithConn(t, serverConn)

				require.NoError(t, n.Start())
				defer n.Close()

				defer func() {
					select {
					case err := <-errChan:
						t.Errorf("server error: %v", err)
					default:
					}
				}()

				ctx, cancel := context.WithTimeout(context.Background(), 5*rtt)
				defer cancel()
				conn, err := quic.Dial(
					ctx,
					clientConn,
					serverConn.LocalAddr(),
					getTLSClientConfig(),
					getQuicConfig(nil),
				)
				require.NoError(t, err)
				defer conn.CloseWithError(0, "")

				str, err := conn.AcceptStream(ctx)
				require.NoError(t, err)
				data, err := io.ReadAll(str)
				require.NoError(t, err)
				require.Equal(t, PRData, data)

				require.GreaterOrEqual(t, conn.ConnectionStats().MinRTT, rtt)

				require.GreaterOrEqual(t, conn.ConnectionStats().SmoothedRTT, rtt)
				require.Less(t, conn.ConnectionStats().SmoothedRTT, rtt+time.Millisecond)

				(<-connChan).CloseWithError(0, "")
			})
		})
	}
}

func TestDownloadWithReordering(t *testing.T) {
	addr, errChan := runServerForRTTTest(t)

	for _, rtt := range []time.Duration{
		5 * time.Millisecond,
		30 * time.Millisecond,
	} {
		t.Run(fmt.Sprintf("RTT %s", rtt), func(t *testing.T) {
			t.Cleanup(func() {
				select {
				case err := <-errChan:
					t.Errorf("server error: %v", err)
				default:
				}
			})

			proxy := quicproxy.Proxy{
				Conn:       newUDPConnLocalhost(t),
				ServerAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: addr.(*net.UDPAddr).Port},
				DelayPacket: func(quicproxy.Direction, net.Addr, net.Addr, []byte) time.Duration {
					return randomDuration(rtt/2, rtt*3/2) / 2
				},
			}
			require.NoError(t, proxy.Start())
			t.Cleanup(func() { proxy.Close() })

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := quic.Dial(
				ctx,
				newUDPConnLocalhost(t),
				proxy.LocalAddr(),
				getTLSClientConfig(),
				getQuicConfig(nil),
			)
			require.NoError(t, err)
			defer conn.CloseWithError(0, "")

			str, err := conn.AcceptStream(ctx)
			require.NoError(t, err)
			data, err := io.ReadAll(str)
			require.NoError(t, err)
			require.Equal(t, PRData, data)
		})
	}
}
