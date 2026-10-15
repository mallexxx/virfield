package baremetal

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

// A real SSH peer completes authentication but never acknowledges a channel.
func stalledChannels(t *testing.T, host ssh.Signer) (string, <-chan string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted, opened := make(chan net.Conn, 1), make(chan string, 1)
	closeServer := sync.OnceFunc(func() {
		listener.Close()
		select {
		case raw := <-accepted:
			raw.Close()
		default:
		}
	})
	t.Cleanup(closeServer)
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(host)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- raw
		defer raw.Close()
		conn, channels, requests, err := ssh.NewServerConn(raw, config)
		if err != nil {
			return
		}
		defer conn.Close()
		go ssh.DiscardRequests(requests)
		for channel := range channels {
			opened <- channel.ChannelType()
		}
	}()
	return listener.Addr().String(), opened, closeServer
}

func TestNodeCancellationInterruptsUnacknowledgedSSHChannels(t *testing.T) {
	for _, operation := range []string{"command", "forward"} {
		t.Run(operation, func(t *testing.T) {
			host := newSigner(t)
			address, opened, closeServer := stalledChannels(t, host)
			ip, port, _ := net.SplitHostPort(address)
			node := Node{ID: "fixture", Address: ip, User: "fixture", HostKeys: []string{authorized(host)}}
			dial := sshDialer(newSigner(t), port)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if operation == "command" {
					s, err := dial(ctx, node)
					if err == nil {
						defer s.Close()
						_, err = s.Run(ctx, "fixed-probe", "")
					}
					done <- err
					return
				}
				runtime := New([]Node{node}, dial)
				conn, err := runtime.DialGuest(ctx, domain.Lease{Location: node.ID, SSHPort: 22100}, "")
				if conn != nil {
					conn.Close()
				}
				done <- err
			}()
			select {
			case <-opened:
			case <-time.After(2 * time.Second):
				closeServer()
				t.Fatal("Fixture never reached channel open")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("Cancellation lost its cause", err)
				}
			case <-time.After(time.Second):
				closeServer()
				<-done
				t.Fatal("Node operation ignored cancellation while opening an SSH channel")
			}
		})
	}
}

func TestNodeCancellationInterruptsSSHHandshakeWithoutADeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		raw, err := listener.Accept()
		if err == nil {
			accepted <- raw // Keep the peer open without sending an SSH banner.
		}
	}()
	address, port, _ := net.SplitHostPort(listener.Addr().String())
	host := newSigner(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	client := newSigner(t)
	go func() {
		s, err := sshDialer(client, port)(ctx, Node{Address: address, User: "fixture", HostKeys: []string{authorized(host)}})
		if s != nil {
			s.Close()
		}
		done <- err
	}()
	var raw net.Conn
	select {
	case raw = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("Fixture never accepted the handshake")
	}
	defer raw.Close()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("Handshake cancellation lost its cause", err)
		}
	case <-time.After(time.Second):
		raw.Close()
		<-done
		t.Fatal("SSH handshake ignored cancellation")
	}
}
