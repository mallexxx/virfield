// Package wol sends Wake-on-LAN magic packets to an allowlisted broadcast
// address. It is shared by every runtime that can wake a physical host.
package wol

import (
	"bytes"
	"context"
	"errors"
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Packet builds the magic packet: six 0xff bytes, then the MAC 16 times.
func Packet(mac string) ([]byte, error) {
	hardware, err := net.ParseMAC(mac)
	if err != nil || len(hardware) != 6 {
		return nil, errors.New("invalid Wake-on-LAN MAC")
	}
	packet := bytes.Repeat([]byte{0xff}, 6)
	for range 16 {
		packet = append(packet, hardware...)
	}
	return packet, nil
}

// Send broadcasts the packet three times to broadcast (host:port).
func Send(ctx context.Context, mac, broadcast string) error {
	packet, err := Packet(mac)
	if err != nil {
		return err
	}
	address, err := net.ResolveUDPAddr("udp4", broadcast)
	if err != nil {
		return err
	}
	connection, err := net.DialUDP("udp4", nil, address)
	if err != nil {
		return err
	}
	defer connection.Close()
	raw, err := connection.SyscallConn()
	if err != nil {
		return err
	}
	var socketErr error
	if err := raw.Control(func(fd uintptr) { socketErr = unix.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1) }); err != nil {
		return err
	}
	if socketErr != nil {
		return socketErr
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetWriteDeadline(deadline)
	} else {
		_ = connection.SetWriteDeadline(time.Now().Add(2 * time.Second))
	}
	for range 3 {
		if _, err := connection.Write(packet); err != nil {
			return err
		}
	}
	return nil
}
